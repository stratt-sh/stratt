// Resolution chains for each universal command, per requirements.md §3.
//
// Each resolveXxx returns the first matching Engine, or nil if no chain
// entry matched.  Order matters: chains are documented as first-match-wins.
package capability

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/stratt-sh/stratt/internal/detect"
)

// uvAllFlags are the flags stratt passes to `uv run` and `uv sync` in
// Python+UV projects.  Without these, uv only resolves the default
// (non-grouped) deps — silently skipping anything declared in
// `[project.optional-dependencies]` or `[tool.uv.dependency-groups]`.
// Projects commonly use extras/groups for dev/test/docs tooling, so
// stratt opts into the full set by default.
var uvAllFlags = []string{"--all-extras", "--all-groups"}

// resolveBuild — see requirements.md §3 "build" chain.
func (r *Resolver) resolveBuild() Engine {
	switch {
	case r.HasStack("ansible-collection"):
		// `--force` overwrites the existing tarball in dist/ so repeated
		// builds in CI are idempotent.
		return &execEngine{
			tool: "ansible-galaxy",
			argv: []string{"collection", "build", "--force", "--output-path", "dist"},
		}
	case r.HasStack("python+uv"):
		return &execEngine{tool: "uv", argv: []string{"build"}}
	case r.HasStack("go") && r.fileExists(".goreleaser.yaml", ".goreleaser.yml"):
		return &execEngine{tool: "goreleaser", argv: []string{"build", "--snapshot", "--clean"}}
	case r.HasStack("go"):
		// Without goreleaser we emit a plain `go build`. Version/commit/date
		// ldflags are injected by the runner once the bump engine knows the
		// project version — for now this is a vanilla build.
		return &execEngine{tool: "go", argv: []string{"build", "./..."}, display: "go build ./..."}
	case r.HasStack("node+npm"):
		return &execEngine{tool: "npm", argv: []string{"run", "build"}}
	case r.HasStack("php"):
		return &execEngine{tool: "composer", argv: []string{"install"}}
	case r.HasStack("docker"):
		return &execEngine{tool: "docker", argv: []string{"build", "."}, display: "docker build ."}
	}
	return nil
}

// ResolveBuildVerify returns the build engine `stratt release` runs to
// catch build breakage before a tag is pushed — the failure GitHub
// Actions would otherwise hit *after* the tag is immutable.
//
// For a Go + goreleaser repo it runs goreleaser itself, so the release
// config is exercised exactly as CI will exercise it — but with
// --single-target, which builds only the host GOOS/GOARCH instead of
// cross-compiling every target.  That's the slow part of a full build,
// and a pure-Go (CGO_ENABLED=0) project rarely breaks per-platform, so
// one target is a good speed/coverage trade.  The snapshot artifacts land
// in the gitignored dist/, so the working tree stays clean for release's
// post-check.
//
// Without goreleaser, a plain `go build ./...` is the compile check
// (it writes no binaries for a multi-package build).  Returns nil for
// stacks where no cheap verification is defined, so the caller can skip.
func (r *Resolver) ResolveBuildVerify() Engine {
	switch {
	case r.HasStack("go") && r.fileExists(".goreleaser.yaml", ".goreleaser.yml"):
		return &execEngine{tool: "goreleaser", argv: []string{"build", "--snapshot", "--clean", "--single-target"}}
	case r.HasStack("go"):
		return &execEngine{tool: "go", argv: []string{"build", "./..."}, display: "go build ./..."}
	}
	return nil
}

// resolveTest — see requirements.md §3 "test" chain.
func (r *Resolver) resolveTest() Engine {
	switch {
	case r.hasAnsibleStack():
		// `ansible-lint --strict` is the closest universal default for
		// Ansible "test" — sanity tests (`ansible-test sanity`) require
		// the collection to be installed in a specific path layout and
		// aren't safe to invoke without configuration.  Repos with
		// molecule or playbook syntax-check suites can override via
		// [tasks.test] in stratt.toml.
		return &execEngine{tool: "ansible-lint", argv: []string{"--strict"}}
	case r.HasStack("python+uv"):
		return &execEngine{tool: "uv", argv: append([]string{"run"}, append(uvAllFlags, "pytest")...)}
	case r.HasStack("go"):
		return &execEngine{tool: "go", argv: []string{"test", "./..."}}
	case r.HasStack("node+npm"):
		return &execEngine{tool: "npm", argv: []string{"test"}}
	case r.HasStack("php"):
		// composer scripts are project-specific; the safe default is
		// `composer test` which fails clearly if no script is defined.
		return &execEngine{tool: "composer", argv: []string{"test"}}
	}
	return nil
}

// resolveLint — see requirements.md §3 "lint" chain.
//
// Stratt is opinionated: `lint` runs the repo's configured linter in
// its fixing mode where one exists.  We call the tools the repo
// already opted into, with the configuration the repo already has.
// Repos that want check-only behavior (typical for CI) use
// `stratt lint --check`, which dispatches to ResolveLintCheck.
func (r *Resolver) resolveLint() Engine {
	return r.lintEngine(true)
}

// ResolveLintCheck is the check-only sibling to lint resolution — same
// chain, same tool family, but with the auto-fix flag stripped.  Used
// by `stratt lint --check` for CI where you want a non-mutating gate.
//
// Like Resolve, it fans out across subprojects — each member
// resolves its own check-only engine so a monorepo's `stratt lint
// --check` stays non-mutating in every subdirectory.
func (r *Resolver) ResolveLintCheck() Engine {
	return r.fanOut("lint", r.lintEngine(false), func(m *Resolver) Engine {
		return m.ResolveLintCheck()
	})
}

// lintEngine factors the chain so both modes share the same matching
// logic.  Passing fix=false yields the check-only invocation.
//
// Language lint is first-match-wins.  `actionlint` is *additionally*
// composed in when both (a) workflows exist and (b) actionlint is on
// PATH — gating on availability avoids breaking CI runs that don't
// have actionlint installed, while still picking it up automatically
// on machines that do.  `stratt doctor` surfaces the conditional skip
// so it's not silent.
func (r *Resolver) lintEngine(fix bool) Engine {
	primary := r.languageLintEngine(fix)
	var secondary Engine
	if r.hasGitHubWorkflows() && available("actionlint") {
		secondary = &execEngine{tool: "actionlint", argv: []string{}, display: "actionlint"}
	}
	switch {
	case primary != nil && secondary != nil:
		return &multiEngine{engines: []Engine{primary, secondary}}
	case primary != nil:
		return primary
	case secondary != nil:
		return secondary
	}
	return nil
}

// ActionlintAvailable reports whether the lint chain would compose
// actionlint into the lint step for this repo.  Returns (workflowsExist,
// toolAvailable) so `stratt doctor` can show a one-line note when the
// repo has workflows but actionlint isn't installed.
func (r *Resolver) ActionlintAvailable() (workflowsExist, toolAvailable bool) {
	return r.hasGitHubWorkflows(), available("actionlint")
}

// SubmoduleStatus reports submodule state for `stratt doctor`.
// `declared` is the number of submodules in .gitmodules; `uninitialized`
// is how many of those haven't been checked out (`git submodule status`
// line beginning with `-`).  When .gitmodules is absent or git is not
// on PATH, both return zero.
//
// Doctor surfaces a one-line advisory when uninitialized > 0, pointing
// users at `stratt setup` (which now composes the init step in).
func (r *Resolver) SubmoduleStatus() (declared, uninitialized int) {
	if !r.fileExists(".gitmodules") {
		return 0, 0
	}
	if !available("git") {
		return 0, 0
	}
	// Bounded so a wedged filesystem or a credential prompt can't hang
	// `stratt doctor` indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", r.root, "submodule", "status")
	out, err := cmd.Output()
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		declared++
		if strings.HasPrefix(line, "-") {
			uninitialized++
		}
	}
	return declared, uninitialized
}

// languageLintEngine returns the per-language lint engine — the
// historical first-match-wins chain.  Split out so lintEngine can
// compose it with non-language linters (actionlint) without
// duplicating the switch.
func (r *Resolver) languageLintEngine(fix bool) Engine {
	switch {
	case r.hasAnsibleStack():
		argv := []string{}
		if fix {
			argv = append(argv, "--fix")
		}
		return &execEngine{tool: "ansible-lint", argv: argv}
	case r.HasStack("node+npm"):
		return &execEngine{tool: "npm", argv: []string{"run", "lint"}}
	case r.HasStack("python+uv"):
		argv := append([]string{"run"}, uvAllFlags...)
		argv = append(argv, "ruff", "check")
		if fix {
			argv = append(argv, "--fix")
		}
		return &execEngine{tool: "uv", argv: argv}
	case r.HasStack("go") && available("golangci-lint"):
		argv := []string{"run"}
		if fix {
			argv = append(argv, "--fix")
		}
		return &execEngine{tool: "golangci-lint", argv: argv}
	case r.HasStack("go"):
		// `go vet` has no fix mode; the flag is a no-op for it.
		return &execEngine{tool: "go", argv: []string{"vet", "./..."}}
	case r.HasStack("php"):
		return &execEngine{tool: "composer", argv: []string{"lint"}}
	}
	return nil
}

// resolveFormat — see requirements.md §3 "format" chain.
//
// Ansible has no separable formatter — `ansible-lint --fix` is the
// closest analog (it applies the auto-fixable subset of its rules).
// Pointing format at it means `stratt format` does what users expect:
// rewrite the files in-place to fix style issues.  Note that lint also
// resolves to `ansible-lint --fix` by default, so `stratt all` runs
// the tool twice on Ansible repos; the second pass is fast (nothing
// left to fix) and idempotent.  This guard also takes priority over
// python+uv, which would otherwise resolve to `ruff format` for repos
// using uv as a tooling layer.
func (r *Resolver) resolveFormat() Engine {
	if r.hasAnsibleStack() {
		return &execEngine{tool: "ansible-lint", argv: []string{"--fix"}}
	}
	switch {
	case r.HasStack("node+npm"):
		return &execEngine{tool: "npm", argv: []string{"run", "format"}}
	case r.HasStack("python+uv"):
		return &execEngine{tool: "uv", argv: append([]string{"run"}, append(uvAllFlags, "ruff", "format")...)}
	case r.HasStack("go"):
		return &execEngine{tool: "gofmt", argv: []string{"-w", "."}, display: "gofmt -w ."}
	}
	return nil
}

// resolveSetup — first-time project setup.  For python+uv, also tries
// `uv self update` first so the user's uv binary stays current.  Soft
// failure (`;` not `&&`) so brew-installed uv (which can't self-update)
// keeps working — the user sees a one-line "use brew upgrade uv" note
// but the sync still runs.
//
// When .gitmodules is present, `git submodule update --init --recursive`
// is composed in ahead of the language step.  Setup is the "make this
// repo workable from a fresh clone" command; missing submodules cause
// late, cryptic failures (e.g. a Hugo theme submodule that leaves
// shortcodes undefined) so we pull them eagerly.
func (r *Resolver) resolveSetup() Engine {
	var inner Engine
	switch {
	case r.HasStack("python+uv"):
		inner = &shellEngine{
			line:    "uv self update; uv sync --all-extras --all-groups",
			display: "uv self update (best-effort) && uv sync --all-extras --all-groups",
		}
	case r.HasStack("node+npm"):
		inner = &execEngine{tool: "npm", argv: []string{"ci"}}
	case r.HasStack("go"):
		inner = &execEngine{tool: "go", argv: []string{"mod", "download"}}
	case r.HasStack("php"):
		inner = &execEngine{tool: "composer", argv: []string{"install"}}
	case r.HasStack("ansible-playbook") && r.firstExisting("requirements.yml", "requirements.yaml") != "":
		// Playbook/inventory repos: pull collection + role deps so the
		// playbooks can actually run.  Collections take precedence over
		// roles; both end up under the resolved collections path.
		inner = &execEngine{
			tool: "ansible-galaxy",
			argv: []string{"install", "-r", r.firstExisting("requirements.yml", "requirements.yaml")},
		}
	}
	return r.composeWithSubmoduleInit(inner)
}

// composeWithSubmoduleInit prepends a `git submodule update --init
// --recursive` step to inner when the repo declares submodules.
// Returns inner unchanged when no .gitmodules is present, or when
// inner is nil (no language step to compose with — submodule init
// alone is enough to constitute setup).
func (r *Resolver) composeWithSubmoduleInit(inner Engine) Engine {
	if !r.fileExists(".gitmodules") {
		return inner
	}
	sub := &execEngine{
		tool:    "git",
		argv:    []string{"submodule", "update", "--init", "--recursive"},
		display: "git submodule update --init --recursive",
	}
	if inner == nil {
		return sub
	}
	return &multiEngine{engines: []Engine{sub, inner}}
}

// resolveSync — sync deps from lockfile.  Composes with submodule
// init/update (--init --recursive is idempotent: a no-op when
// submodules are already initialized; pulls drifted pins otherwise).
func (r *Resolver) resolveSync() Engine {
	var inner Engine
	switch {
	case r.HasStack("python+uv"):
		inner = &execEngine{tool: "uv", argv: append([]string{"sync"}, uvAllFlags...)}
	case r.HasStack("node+npm"):
		inner = &execEngine{tool: "npm", argv: []string{"ci"}}
	case r.HasStack("go"):
		inner = &execEngine{tool: "go", argv: []string{"mod", "download"}}
	case r.HasStack("php"):
		inner = &execEngine{tool: "composer", argv: []string{"install", "--no-dev"}}
	case r.HasStack("ansible-playbook") && r.firstExisting("requirements.yml", "requirements.yaml") != "":
		// `--force` so a re-sync picks up pin moves in requirements.yml
		// instead of silently keeping the previously-installed version.
		inner = &execEngine{
			tool: "ansible-galaxy",
			argv: []string{"install", "-r", r.firstExisting("requirements.yml", "requirements.yaml"), "--force"},
		}
	}
	return r.composeWithSubmoduleInit(inner)
}

// resolveLock — update lockfile from manifest.
func (r *Resolver) resolveLock() Engine {
	switch {
	case r.HasStack("python+uv"):
		return &execEngine{tool: "uv", argv: []string{"lock"}}
	case r.HasStack("node+npm"):
		return &execEngine{tool: "npm", argv: []string{"install"}}
	case r.HasStack("go"):
		return &execEngine{tool: "go", argv: []string{"mod", "tidy"}}
	case r.HasStack("php"):
		return &execEngine{tool: "composer", argv: []string{"update", "--lock"}}
	}
	return nil
}

// resolveUpgrade — upgrade all dependencies, then re-sync so the local
// env reflects the upgraded lockfile.  For python+uv, also tries
// `uv self update` first (best effort, see resolveSetup).  Mirrors
// Make's `upgrade` plus the SETUP_EXTRAS `uv-self-update` semantic.
func (r *Resolver) resolveUpgrade() Engine {
	switch {
	case r.HasStack("python+uv"):
		return &shellEngine{
			line:    "uv self update; uv lock --upgrade && uv sync --all-extras --all-groups",
			display: "uv self update (best-effort) && uv lock --upgrade && uv sync --all-extras --all-groups",
		}
	case r.HasStack("node+npm"):
		return &shellEngine{line: "npm update && npm install", display: "npm update && npm install"}
	case r.HasStack("go"):
		return &shellEngine{line: "go get -u ./... && go mod tidy", display: "go get -u ./... && go mod tidy"}
	case r.HasStack("php"):
		return &execEngine{tool: "composer", argv: []string{"update"}}
	}
	return nil
}

// resolveClean — multi-stack cleanup is implemented as its own
// subcommand (`stratt clean`) since it has different fan-out semantics
// from the other universal commands.  This entry is delegateEngine for
// doctor display.
func (r *Resolver) resolveClean() Engine {
	return &delegateEngine{
		display:     "remove build/cache artifacts per detected stacks",
		delegateCmd: "stratt clean",
	}
}

// resolveRelease — see requirements.md §3 "release" chain.
//
//  1. Bump-my-version config present anywhere → native bump engine
//  2. .goreleaser.yaml present (and no bump config) → tag-only mode
//  3. Otherwise → tag-only mode
//
// `stratt release` is a custom-shape subcommand, so these engines
// are display-only (delegateEngine).  Their Status reflects that the
// subcommand is available.
func (r *Resolver) resolveRelease() Engine {
	switch {
	case r.hasBumpConfig():
		return &delegateEngine{
			display:     "native bump engine (reads [tool.bumpversion])",
			delegateCmd: "stratt release",
		}
	case r.fileExists(".goreleaser.yaml", ".goreleaser.yml"):
		return &delegateEngine{
			display:     "tag-only release (CI runs goreleaser on tag-push)",
			delegateCmd: "stratt release",
		}
	case r.HasStack("ansible-collection"):
		// Ansible collections version in galaxy.yml.  The bump loader
		// synthesizes a config from galaxy.yml when no explicit
		// [bump] / [tool.bumpversion] is present, so this delegates
		// to the same `stratt release` subcommand as other stacks.
		return &delegateEngine{
			display:     "native bump engine (reads galaxy.yml version)",
			delegateCmd: "stratt release",
		}
	case r.HasStack("go") || r.HasStack("node+npm") || r.HasStack("python+uv") || r.HasStack("php"):
		return &delegateEngine{
			display:     "tag-only release",
			delegateCmd: "stratt release",
		}
	}
	return nil
}

// resolveDeploy — Kustomize image bump is the only deploy engine in v1.
// `stratt deploy` is a custom-shape subcommand (it takes positional
// args), so this is a delegateEngine for doctor display.
func (r *Resolver) resolveDeploy() Engine {
	if r.HasStack("kustomize") {
		return &delegateEngine{
			display:     "kustomize image bump (deploy/overlays/<env>/kustomization.yaml)",
			delegateCmd: "stratt deploy",
		}
	}
	return nil
}

// resolveDocs — first matching documentation toolchain.
func (r *Resolver) resolveDocs() Engine {
	switch {
	case r.HasStack("mkdocs"):
		return &execEngine{tool: "mkdocs", argv: []string{"build"}}
	case r.HasStack("sphinx"):
		// Output to docs/_build/html (matches Make's `cd docs && sphinx-build -b html . _build/html`).
		// Keeping the build output inside docs/ means `stratt clean`'s
		// docs/_build/ removal path picks it up uniformly.
		return &execEngine{tool: "sphinx-build", argv: []string{"-b", "html", "docs", "docs/_build/html"}}
	case r.HasStack("hugo"):
		src := detect.FindHugoSource(r.root)
		argv := []string{"--minify"}
		if src != "" && src != "." {
			argv = append([]string{"--source", src}, argv...)
		}
		return &execEngine{tool: "hugo", argv: argv}
	}
	return nil
}

// resolveStyle — composite of format + lint.  Only resolves when both
// constituents have engines (i.e., the project has formatters and
// linters available).
//
// Membership is tested through Resolve (not the raw resolveFormat/
// resolveLint chains) so that in a monorepo, where format and lint resolve
// by fanning out across subprojects rather than at the root, `style` still
// composes them.  For single-stack repos Resolve and the raw chains agree,
// so behavior is unchanged.
func (r *Resolver) resolveStyle() Engine {
	if r.Resolve("format").Engine == nil || r.Resolve("lint").Engine == nil {
		return nil
	}
	return &compositeEngine{
		display: "format + lint",
		members: []string{"format", "lint"},
	}
}

// resolveAll — composite of every detected verification step that's
// applicable.  Per project policy this is "everything detected" by
// default; users override via [tasks.all] in stratt.toml when they
// want a narrower set.
//
// Membership: sync, format, lint, test, docs (in that order, each
// included only if its constituent engine resolves).  `sync` runs
// first so the env is current before tests — implicitly covers the
// "uv.lock consistent with pyproject.toml" check.
//
// Format/lint dedup: when both resolve to the same root tool — most
// notably Ansible, where `format` and `lint` both invoke
// `ansible-lint --fix` — `format` is dropped from the execution list
// so the tool runs once.  The displayed composition still mentions
// `format` (rendered as `format(via lint)`) so users see that the
// formatting step exists conceptually — it's just folded into lint
// to avoid redundant work.  `stratt format` and `stratt lint` remain
// individually invocable.
func (r *Resolver) resolveAll() Engine {
	var members []string      // what actually runs
	var displayParts []string // what's shown to the user (may include subsumed steps)

	add := func(name string) {
		members = append(members, name)
		displayParts = append(displayParts, name)
	}
	addDisplayOnly := func(label string) {
		displayParts = append(displayParts, label)
	}

	// Membership is tested through Resolve so the composite picks up
	// fanned-out subproject verbs in a monorepo as well as root verbs in a
	// single-stack repo (where the two paths agree).
	if r.Resolve("sync").Engine != nil {
		add("sync")
	}
	format := r.Resolve("format").Engine
	lint := r.Resolve("lint").Engine
	switch {
	case format != nil && lint != nil && lintSubsumes(lint, format):
		addDisplayOnly("format(via lint)")
	case format != nil:
		add("format")
	}
	if lint != nil {
		add("lint")
	}
	if r.Resolve("test").Engine != nil {
		add("test")
	}
	if r.Resolve("docs").Engine != nil {
		add("docs")
	}
	if len(members) == 0 {
		return nil
	}
	return &compositeEngine{
		display: strings.Join(displayParts, " + "),
		members: members,
	}
}

// lintSubsumes reports whether the lint engine already invokes the
// same underlying tool that format would.  Used by resolveAll to drop
// `format` when running `lint` would redundantly do the same fixing
// pass — the canonical case is Ansible, where both resolve to
// `ansible-lint --fix` (lint also composes actionlint on top).
//
// Comparison is by Tool() for single-tool engines; multiEngines are
// checked against each of their sub-engines via MultiTooler.  This
// matches "same tool, same fixing intent" without depending on flag
// string equality (which is brittle).
func lintSubsumes(lint, format Engine) bool {
	if lint == nil || format == nil {
		return false
	}
	ft, ok := format.(Tooler)
	if !ok || ft.Tool() == "" {
		return false
	}
	formatTool := ft.Tool()
	if mt, ok := lint.(MultiTooler); ok {
		for _, t := range mt.Tools() {
			if t == formatTool {
				return true
			}
		}
		return false
	}
	if lt, ok := lint.(Tooler); ok {
		return lt.Tool() == formatTool
	}
	return false
}

// hasAnsibleStack reports whether any of the Ansible stacks (collection,
// role, playbook) are detected.  Used by the lint / format / test /
// clean chains since the Ansible tooling (ansible-lint, ansible-galaxy)
// is shared across all three shapes.
func (r *Resolver) hasAnsibleStack() bool {
	return r.HasStack("ansible-collection") ||
		r.HasStack("ansible-role") ||
		r.HasStack("ansible-playbook")
}

// fileExists reports whether any of the given filenames exist in the repo root.
func (r *Resolver) fileExists(names ...string) bool {
	return r.firstExisting(names...) != ""
}

// firstExisting returns the first of names that exists under the repo
// root, or "" if none do.  Used when a command must reference the exact
// filename that's present (e.g. requirements.yml vs requirements.yaml).
func (r *Resolver) firstExisting(names ...string) string {
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(r.root, n)); err == nil {
			return n
		}
	}
	return ""
}

// hasGitHubWorkflows reports whether .github/workflows/ contains at
// least one .yml/.yaml file.  Distinguishes a "workflows" actions repo
// from a composite-action-only repo (action.yml at root).
func (r *Resolver) hasGitHubWorkflows() bool {
	entries, err := os.ReadDir(filepath.Join(r.root, ".github", "workflows"))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml") {
			return true
		}
	}
	return false
}

// hasBumpConfig reports whether any recognized bump-my-version-style
// configuration exists in the repo.  See R2.4.7 for the full chain.
func (r *Resolver) hasBumpConfig() bool {
	if r.fileExists(".bumpversion.toml", ".bumpversion.cfg") {
		return true
	}
	// `[tool.bumpversion]` or `[tool.stratt.bump]` in pyproject.toml, or
	// `[bump]` in stratt.toml.  Done with a coarse byte scan for now;
	// the config loader will do this properly once it lands.
	for _, file := range []string{"pyproject.toml", "stratt.toml"} {
		path := filepath.Join(r.root, file)
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		body := string(b)
		switch file {
		case "pyproject.toml":
			if containsSection(body, "tool.bumpversion") || containsSection(body, "tool.stratt.bump") {
				return true
			}
		case "stratt.toml":
			if containsSection(body, "bump") {
				return true
			}
		}
	}
	return false
}

// containsSection reports whether body contains a TOML section header
// matching name.  Tolerant of whitespace around the brackets — sufficient
// as a heuristic before the real config loader lands and replaces this.
func containsSection(body, name string) bool {
	header := "[" + name + "]"
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == header {
			return true
		}
	}
	return false
}
