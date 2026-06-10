// Package bump is stratt's native version-bump engine.
//
// Per R2.4.6, stratt does NOT shell out to bump-my-version.  This
// package implements the bump → commit → tag → push flow natively,
// reading the same on-disk config formats existing repos already use
// (R2.4.7) so adoption requires zero migration:
//
//   - native:   [bump] in stratt.toml  OR  [tool.stratt.bump] in pyproject.toml
//   - compat:   [tool.bumpversion] in pyproject.toml
//   - compat:   .bumpversion.toml
//   - compat:   .bumpversion.cfg  (legacy INI — recognized but emits a deprecation note)
//
// v1 feature set (R2.4.10): semver patch/minor/major bumps, per-file
// search/replace with {current_version}/{new_version} template
// substitution, configurable commit message and tag prefix, optional
// commit+tag+push.  pre/post hooks, custom serialize/parse formats,
// and sign_tags are deferred.
package bump

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Kind is the bump granularity.
type Kind int

const (
	Patch Kind = iota
	Minor
	Major
)

func (k Kind) String() string {
	switch k {
	case Patch:
		return "patch"
	case Minor:
		return "minor"
	case Major:
		return "major"
	}
	return "?"
}

// KindFromString parses one of "patch", "minor", "major" (case-insensitive).
func KindFromString(s string) (Kind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "patch":
		return Patch, nil
	case "minor":
		return Minor, nil
	case "major":
		return Major, nil
	}
	return 0, fmt.Errorf("invalid bump kind %q (want patch|minor|major)", s)
}

// Config is the loaded bump configuration, normalized across the four
// supported on-disk formats.
type Config struct {
	// Source is the file the config was read from, for error messages.
	Source string

	// CurrentVersion is the version string before the bump.
	CurrentVersion string

	// SearchTemplate / ReplaceTemplate are the top-level defaults for
	// per-file find-and-replace.  Each FileEntry can override.  Templates
	// can contain {current_version} and {new_version}.
	SearchTemplate  string
	ReplaceTemplate string

	// Files is the list of per-file edits.
	Files []FileEntry

	// Commit, Tag — control whether to create a git commit / tag.
	Commit bool
	Tag    bool

	// MessageTemplate is the commit message.  Templates {current_version}
	// and {new_version} substitute.
	MessageTemplate string

	// TagNameTemplate is the tag name.  Same template variables.
	TagNameTemplate string
}

// FileEntry describes one file to edit during a bump.
type FileEntry struct {
	Filename string
	Search   string // empty → inherit from Config.SearchTemplate
	Replace  string // empty → inherit from Config.ReplaceTemplate
	// ReplaceAll, when true, replaces every occurrence of Search in the
	// file.  Default (false) replaces only the first match — matching
	// bump-my-version's behavior.  Useful for files like READMEs that
	// reference the version in multiple places.
	ReplaceAll bool
}

// Plan is the result of computing a bump.  All fields are populated
// without touching the filesystem; Apply commits the changes.
type Plan struct {
	Cfg           *Config
	OldVersion    string
	NewVersion    string
	FileChanges   []FileChange
	CommitMessage string
	TagName       string
}

// FileChange describes a single edit Apply will make.
type FileChange struct {
	Path     string
	OldChunk string // search string with templates substituted
	NewChunk string // replace string with templates substituted
	// Found reports whether the search string was found in the file.
	// A FileChange with Found == false will fail Apply unless the
	// caller filters it out, matching bump-my-version's
	// `ignore_missing_version = false` default.
	Found bool
	// ReplaceAll mirrors FileEntry.ReplaceAll — when true, every
	// occurrence of OldChunk in the file is rewritten, not just the
	// first.
	ReplaceAll bool
}

// Compute returns a Plan for bumping cfg by kind (a normal release).
// It is a thin wrapper over ComputeAction; see that for prereleases.
func Compute(cfg *Config, kind Kind, root string) (*Plan, error) {
	return ComputeAction(cfg, Action{Kind: kind, Op: OpRelease}, root)
}

// ComputeAction returns a Plan for applying action to cfg.  The plan is
// deterministic — calling it multiple times yields identical output —
// and side-effect-free, so callers can show a preview before applying.
func ComputeAction(cfg *Config, action Action, root string) (*Plan, error) {
	if cfg.CurrentVersion == "" {
		return nil, errors.New("bump config has no current_version")
	}
	next, err := nextVersion(cfg.CurrentVersion, action)
	if err != nil {
		return nil, err
	}
	plan := &Plan{
		Cfg:        cfg,
		OldVersion: cfg.CurrentVersion,
		NewVersion: next,
	}
	for _, fe := range cfg.Files {
		search := orDefault(fe.Search, cfg.SearchTemplate, "{current_version}")
		replace := orDefault(fe.Replace, cfg.ReplaceTemplate, "{new_version}")
		oldChunk := substitute(search, cfg.CurrentVersion, next)
		newChunk := substitute(replace, cfg.CurrentVersion, next)

		path := filepath.Join(root, fe.Filename)
		found, err := chunkPresent(path, oldChunk)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		plan.FileChanges = append(plan.FileChanges, FileChange{
			Path:       path,
			OldChunk:   oldChunk,
			NewChunk:   newChunk,
			Found:      found,
			ReplaceAll: fe.ReplaceAll,
		})
	}
	plan.CommitMessage = substitute(orDefault(cfg.MessageTemplate, "",
		"Bump version: {current_version} → {new_version}"), cfg.CurrentVersion, next)
	plan.TagName = substitute(orDefault(cfg.TagNameTemplate, "", "v{new_version}"), cfg.CurrentVersion, next)
	return plan, nil
}

// Apply writes the file changes from plan.  Returns ErrMissingVersion
// if any FileChange has Found == false, mirroring bump-my-version's
// strict default.
//
// Apply does NOT commit, tag, or push — those steps live in a separate
// git helper so this package stays free of git side-effects.
func Apply(plan *Plan) error {
	for _, change := range plan.FileChanges {
		if !change.Found {
			return fmt.Errorf("%w: search string not found in %s",
				ErrMissingVersion, change.Path)
		}
	}
	for _, change := range plan.FileChanges {
		data, err := os.ReadFile(change.Path)
		if err != nil {
			return err
		}
		n := 1
		if change.ReplaceAll {
			n = -1
		}
		updated := strings.Replace(string(data), change.OldChunk, change.NewChunk, n)
		if updated == string(data) {
			// Re-check protects against a race between Compute and Apply.
			return fmt.Errorf("%w: search string disappeared from %s before apply",
				ErrMissingVersion, change.Path)
		}
		if err := os.WriteFile(change.Path, []byte(updated), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// ErrMissingVersion is returned by Apply (or surfaced via Compute when
// Found == false) when one of the configured search strings is absent
// from its target file.
var ErrMissingVersion = errors.New("bump search string not found")

// bumpSemver computes the next semver string for kind from current.
// Only MAJOR.MINOR.PATCH form is supported in v1; pre-release and
// build-metadata suffixes are stripped (the new version drops them).
func bumpSemver(current string, kind Kind) (string, error) {
	current = strings.TrimPrefix(current, "v")
	core := current
	if i := strings.IndexAny(current, "-+"); i >= 0 {
		core = current[:i]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("current_version %q is not MAJOR.MINOR.PATCH", current)
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return "", fmt.Errorf("current_version %q: component %q is not numeric", current, p)
		}
		if n < 0 {
			return "", fmt.Errorf("current_version %q: negative component", current)
		}
		nums[i] = n
	}
	switch kind {
	case Major:
		nums[0]++
		nums[1] = 0
		nums[2] = 0
	case Minor:
		nums[1]++
		nums[2] = 0
	case Patch:
		nums[2]++
	}
	return fmt.Sprintf("%d.%d.%d", nums[0], nums[1], nums[2]), nil
}

func substitute(template, oldV, newV string) string {
	s := strings.ReplaceAll(template, "{current_version}", oldV)
	s = strings.ReplaceAll(s, "{new_version}", newV)
	return s
}

func orDefault(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func chunkPresent(path, chunk string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return strings.Contains(string(data), chunk), nil
}

// semverRE matches a normalized MAJOR.MINOR.PATCH version.  Exposed so
// other packages can validate inputs without re-implementing the rule.
var semverRE = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// IsValid reports whether v is a well-formed MAJOR.MINOR.PATCH string.
func IsValid(v string) bool {
	return semverRE.MatchString(strings.TrimPrefix(v, "v"))
}

// --- Prerelease support (R2.4: first-class prereleases) ---

// PreOp identifies a version transition beyond a plain semver bump.
type PreOp int

const (
	OpRelease PreOp = iota // normal release: bump Kind, drop any prerelease
	OpStart                // start a prerelease: bump Kind core, then -<label>.1
	OpIterate              // bump the prerelease counter (…-rc.1 → …-rc.2)
	OpPromote              // finalize: drop the prerelease suffix
	OpRelabel              // switch prerelease label, reset counter to 1
)

// Action fully specifies one version transition.  Kind is the base bump
// (used by OpRelease and OpStart); Label is the prerelease identifier
// (used by OpStart and OpRelabel; empty defaults to DefaultPreLabel).
type Action struct {
	Kind  Kind
	Op    PreOp
	Label string
}

// DefaultPreLabel is the prerelease identifier used when none is given.
const DefaultPreLabel = "rc"

// preLabelRE constrains prerelease labels to an alpha-led alphanumeric
// token (rc, beta, alpha, rc2…) so the serialized X.Y.Z-<label>.<n> form
// round-trips unambiguously.
var preLabelRE = regexp.MustCompile(`^[A-Za-z][0-9A-Za-z]*$`)

// IsPrerelease reports whether v is a stratt-emitted prerelease, i.e. the
// strict X.Y.Z-<label>.<N> form.  Loose suffixes (e.g. "1.2.3-dev") are
// not recognized as iterable prereleases.
func IsPrerelease(v string) bool {
	_, _, _, ok := splitPrerelease(v)
	return ok
}

// splitPrerelease parses "X.Y.Z-<label>.<n>" into its parts.  ok is false
// for any version that isn't exactly that shape.
func splitPrerelease(v string) (core, label string, n int, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	dash := strings.IndexByte(v, '-')
	if dash < 0 {
		return "", "", 0, false
	}
	core = v[:dash]
	rest := v[dash+1:]
	dot := strings.LastIndexByte(rest, '.')
	if dot < 0 {
		return "", "", 0, false
	}
	label = rest[:dot]
	num, err := strconv.Atoi(rest[dot+1:])
	if err != nil || num < 1 {
		return "", "", 0, false
	}
	if !semverRE.MatchString(core) || !preLabelRE.MatchString(label) {
		return "", "", 0, false
	}
	return core, label, num, true
}

// hasSuffix reports whether v carries any prerelease/build suffix.
func hasSuffix(v string) bool {
	return strings.ContainsAny(strings.TrimPrefix(strings.TrimSpace(v), "v"), "-+")
}

// nextVersion computes the new version string for action against current.
func nextVersion(current string, a Action) (string, error) {
	switch a.Op {
	case OpRelease:
		return bumpSemver(current, a.Kind)

	case OpStart:
		if hasSuffix(current) {
			return "", fmt.Errorf("already on a prerelease (%s); use iterate, promote, or relabel", current)
		}
		core, err := bumpSemver(current, a.Kind)
		if err != nil {
			return "", err
		}
		label := a.Label
		if label == "" {
			label = DefaultPreLabel
		}
		if !preLabelRE.MatchString(label) {
			return "", fmt.Errorf("invalid prerelease label %q (want an alpha-led alphanumeric like rc, beta, alpha)", label)
		}
		return fmt.Sprintf("%s-%s.1", core, label), nil

	case OpIterate:
		core, label, n, ok := splitPrerelease(current)
		if !ok {
			return "", errNotPrerelease(current)
		}
		return fmt.Sprintf("%s-%s.%d", core, label, n+1), nil

	case OpPromote:
		core, _, _, ok := splitPrerelease(current)
		if !ok {
			return "", errNotPrerelease(current)
		}
		return core, nil

	case OpRelabel:
		core, _, _, ok := splitPrerelease(current)
		if !ok {
			return "", errNotPrerelease(current)
		}
		label := a.Label
		if label == "" {
			return "", errors.New("relabel needs a label (e.g. `relabel beta`)")
		}
		if !preLabelRE.MatchString(label) {
			return "", fmt.Errorf("invalid prerelease label %q (want an alpha-led alphanumeric like rc, beta, alpha)", label)
		}
		return fmt.Sprintf("%s-%s.1", core, label), nil
	}
	return "", fmt.Errorf("unknown release op %d", a.Op)
}

func errNotPrerelease(current string) error {
	return fmt.Errorf("%s is not a prerelease; start one first (e.g. `stratt release preminor`)", current)
}

// ParseAction interprets release verb tokens — the words after
// `stratt release`, or those typed at the interactive prompt — plus the
// value of the --pre flag, into an Action.
//
// ok is false (with a nil error) when no verb was supplied, signaling the
// caller to prompt.  pre is "" when --pre was absent, "rc" for a bare
// --pre (via the flag's NoOptDefVal), or an explicit label.
//
// Accepted spellings (all equivalent for starting a prerelease):
//
//	preminor                 minor,rc                 minor --pre
//	preminor --pre=beta      minor,beta               minor --pre=beta
//
// plus iterate, promote, and relabel <label> for an in-flight prerelease.
func ParseAction(tokens []string, pre string) (Action, bool, error) {
	var t []string
	for _, x := range tokens {
		if x = strings.TrimSpace(x); x != "" {
			t = append(t, x)
		}
	}
	if len(t) == 0 {
		if pre != "" {
			return Action{}, false, errors.New("--pre needs a base bump (e.g. `release minor --pre` or `release preminor`)")
		}
		return Action{}, false, nil // caller should prompt
	}
	verb := strings.ToLower(t[0])

	// Chained form: minor,rc / patch,beta.
	if base, label, found := strings.Cut(verb, ","); found {
		k, err := KindFromString(base)
		if err != nil {
			return Action{}, false, err
		}
		if pre != "" && pre != label {
			return Action{}, false, fmt.Errorf("conflicting prerelease labels: %q vs --pre=%q", label, pre)
		}
		if label == "" {
			label = DefaultPreLabel
		}
		return Action{Kind: k, Op: OpStart, Label: label}, true, nil
	}

	switch verb {
	case "patch", "minor", "major":
		k, _ := KindFromString(verb)
		if pre != "" {
			return Action{Kind: k, Op: OpStart, Label: pre}, true, nil
		}
		return Action{Kind: k, Op: OpRelease}, true, nil
	case "prepatch", "preminor", "premajor":
		k, _ := KindFromString(strings.TrimPrefix(verb, "pre"))
		label := pre
		if len(t) > 1 { // explicit positional label: `preminor beta`
			label = strings.ToLower(t[1])
		}
		if label == "" {
			label = DefaultPreLabel
		}
		return Action{Kind: k, Op: OpStart, Label: label}, true, nil
	case "iterate":
		if pre != "" {
			return Action{}, false, errors.New("iterate takes no label; use `relabel <label>` to change it")
		}
		return Action{Op: OpIterate}, true, nil
	case "promote":
		return Action{Op: OpPromote}, true, nil
	case "relabel":
		label := pre
		if len(t) > 1 {
			label = strings.ToLower(t[1])
		}
		if label == "" {
			return Action{}, false, errors.New("relabel needs a label (e.g. `relabel beta`)")
		}
		return Action{Op: OpRelabel, Label: label}, true, nil
	}
	return Action{}, false, fmt.Errorf(
		"unknown release verb %q (want patch|minor|major | prepatch|preminor|premajor | iterate|promote|relabel)", t[0])
}
