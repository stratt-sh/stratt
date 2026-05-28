package capability

import (
	"os"
	"path/filepath"
	"testing"
)

// touch writes an empty file inside root, creating parent dirs.
func touch(t *testing.T, root, rel string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %v", err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write %v", err)
	}
}

// writeFile is a small helper for tests that need to put TOML content
// in a file (e.g. to exercise hasBumpConfig).
func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %v", err)
	}
}

// TestBuildChainPythonUV — pyproject.toml + uv.lock wins on build.
func TestBuildChainPythonUV(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "pyproject.toml")
	touch(t, dir, "uv.lock")

	r := New(dir)
	got := r.Resolve("build")
	if got.Engine == nil {
		t.Fatal("expected an engine, got nil")
	}
	if got.Engine.Name() != "uv build" {
		t.Errorf("got %q, want %q", got.Engine.Name(), "uv build")
	}
}

// TestBuildChainGoWithGoReleaser — Go + .goreleaser.yaml selects goreleaser.
func TestBuildChainGoWithGoReleaser(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "go.mod")
	touch(t, dir, ".goreleaser.yaml")

	r := New(dir)
	got := r.Resolve("build")
	if got.Engine == nil {
		t.Fatal("expected an engine, got nil")
	}
	if got.Engine.Name() != "goreleaser build --snapshot --clean" {
		t.Errorf("got %q", got.Engine.Name())
	}
}

// TestBuildChainGoPlain — Go without goreleaser falls through to `go build`.
func TestBuildChainGoPlain(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "go.mod")

	r := New(dir)
	got := r.Resolve("build")
	if got.Engine == nil {
		t.Fatal("expected an engine, got nil")
	}
	if got.Engine.Name() != "go build ./..." {
		t.Errorf("got %q", got.Engine.Name())
	}
}

// TestBuildChainEmptyRepo — no stacks → nil engine.
func TestBuildChainEmptyRepo(t *testing.T) {
	r := New(t.TempDir())
	got := r.Resolve("build")
	if got.Engine != nil {
		t.Errorf("empty repo: expected nil, got %v", got.Engine)
	}
}

// TestTestChainPythonUV — python+uv → uv run pytest.
func TestTestChainPythonUV(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "pyproject.toml")
	touch(t, dir, "uv.lock")

	r := New(dir)
	got := r.Resolve("test")
	if got.Engine == nil || got.Engine.Name() != "uv run --all-extras --all-groups pytest" {
		t.Errorf("got %v", got.Engine)
	}
}

// TestLintFixesByDefault — `stratt lint` should run the configured
// linter in fixing mode where one exists (R2.2).
func TestLintFixesByDefault(t *testing.T) {
	pyDir := t.TempDir()
	touch(t, pyDir, "pyproject.toml")
	touch(t, pyDir, "uv.lock")
	if got := New(pyDir).Resolve("lint"); got.Engine == nil ||
		got.Engine.Name() != "uv run --all-extras --all-groups ruff check --fix" {
		t.Errorf("python lint: got %v", got.Engine)
	}
}

// TestLintCheckOnlyDropsFix — ResolveLintCheck returns the same chain
// but with the auto-fix flag stripped (mirrors `make lint-check`).
func TestLintCheckOnlyDropsFix(t *testing.T) {
	pyDir := t.TempDir()
	touch(t, pyDir, "pyproject.toml")
	touch(t, pyDir, "uv.lock")
	got := New(pyDir).ResolveLintCheck()
	if got == nil {
		t.Fatal("expected check-only engine")
	}
	if got.Name() != "uv run --all-extras --all-groups ruff check" {
		t.Errorf("got %q", got.Name())
	}
}

// TestLintCheckOnlyGolangciLint — Go path also strips --fix.
func TestLintCheckOnlyGolangciLint(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "go.mod")
	got := New(dir).ResolveLintCheck()
	if got == nil {
		t.Fatal("expected engine")
	}
	// On environments where golangci-lint isn't on PATH the chain falls
	// through to `go vet`, which has no fix flag to strip; that's fine.
	if got.Name() != "golangci-lint run" && got.Name() != "go vet ./..." {
		t.Errorf("unexpected check engine: %q", got.Name())
	}
}

// TestTestChainGo — go → `go test ./...`.
func TestTestChainGo(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "go.mod")

	r := New(dir)
	got := r.Resolve("test")
	if got.Engine == nil || got.Engine.Name() != "go test ./..." {
		t.Errorf("got %v", got.Engine)
	}
}

// TestReleaseChainBumpMyVersionInPyproject — bump-my-version config
// inside pyproject.toml wins regardless of language.  The release
// engine is a delegateEngine because `stratt release` is a
// custom-shape subcommand, not a runner-dispatched universal command.
func TestReleaseChainBumpMyVersionInPyproject(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "pyproject.toml")
	touch(t, dir, "uv.lock")
	// Append the bump-my-version section.
	writeFile(t, dir, "pyproject.toml", "[tool.bumpversion]\ncurrent_version = \"1.0.0\"\n")

	r := New(dir)
	got := r.Resolve("release")
	if got.Engine == nil {
		t.Fatal("expected engine, got nil")
	}
	if got.Engine.Status() != StatusReady {
		t.Errorf("expected StatusReady (delegate), got %v", got.Engine.Status())
	}
	if got.Engine.Name() != "native bump engine (reads [tool.bumpversion])" {
		t.Errorf("got %q", got.Engine.Name())
	}
}

// TestReleaseChainBumpMyVersionStandaloneFile — the same recognition
// for .bumpversion.toml (the standalone form used by some repos).
func TestReleaseChainBumpMyVersionStandaloneFile(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "go.mod") // Go repo using bump-my-version
	touch(t, dir, ".bumpversion.toml")

	r := New(dir)
	got := r.Resolve("release")
	if got.Engine == nil {
		t.Fatal("expected engine for go + .bumpversion.toml, got nil")
	}
	if got.Engine.Name() != "native bump engine (reads [tool.bumpversion])" {
		t.Errorf("expected native bump engine; got %q", got.Engine.Name())
	}
}

// TestReleaseChainGoReleaserOnly — a Go repo with goreleaser but no
// bump config → tag-only release (stratt's own pattern).
func TestReleaseChainGoReleaserOnly(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "go.mod")
	touch(t, dir, ".goreleaser.yaml")

	r := New(dir)
	got := r.Resolve("release")
	if got.Engine == nil {
		t.Fatal("expected engine, got nil")
	}
	if got.Engine.Name() != "tag-only release (CI runs goreleaser on tag-push)" {
		t.Errorf("got %q", got.Engine.Name())
	}
}

// TestReleaseChainPlainGo — no bump, no goreleaser → tag-only.
func TestReleaseChainPlainGo(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "go.mod")

	r := New(dir)
	got := r.Resolve("release")
	if got.Engine == nil {
		t.Fatal("expected engine, got nil")
	}
	if got.Engine.Name() != "tag-only release" {
		t.Errorf("got %q", got.Engine.Name())
	}
}

// TestReleaseChainEmpty — no relevant stacks → no release engine.
func TestReleaseChainEmpty(t *testing.T) {
	r := New(t.TempDir())
	got := r.Resolve("release")
	if got.Engine != nil {
		t.Errorf("empty repo: got %v", got.Engine)
	}
}

// TestDeployChainKustomize — kustomize overlay detection wires up deploy.
// The engine is a delegate because `stratt deploy` is a custom-shape
// subcommand that consumes positional args.
func TestDeployChainKustomize(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "deploy/overlays/prod/kustomization.yaml")

	r := New(dir)
	got := r.Resolve("deploy")
	if got.Engine == nil {
		t.Fatal("expected deploy engine, got nil")
	}
	if got.Engine.Status() != StatusReady {
		t.Errorf("expected StatusReady (delegate), got %v", got.Engine.Status())
	}
}

func TestDeployChainNoKustomize(t *testing.T) {
	r := New(t.TempDir())
	got := r.Resolve("deploy")
	if got.Engine != nil {
		t.Errorf("no kustomize: got %v", got.Engine)
	}
}

func TestDocsChainMkDocsAndSphinx(t *testing.T) {
	mk := t.TempDir()
	touch(t, mk, "mkdocs.yml")
	if got := New(mk).Resolve("docs"); got.Engine == nil || got.Engine.Name() != "mkdocs build" {
		t.Errorf("mkdocs: got %v", got.Engine)
	}

	sphinx := t.TempDir()
	touch(t, sphinx, "docs/conf.py")
	// Output to docs/_build/html so `stratt clean`'s sphinx target picks it up.
	if got := New(sphinx).Resolve("docs"); got.Engine == nil || got.Engine.Name() != "sphinx-build -b html docs docs/_build/html" {
		t.Errorf("sphinx: got %v", got.Engine)
	}
}

// TestPyprojectBumpConfigSubsection — `[tool.stratt.bump]` (the future
// native location) is also recognized by hasBumpConfig.
func TestPyprojectBumpConfigSubsection(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pyproject.toml", "[tool.stratt.bump]\ncurrent_version = \"1.0.0\"\n")
	touch(t, dir, "go.mod")

	r := New(dir)
	got := r.Resolve("release")
	if got.Engine == nil || got.Engine.Name() != "native bump engine (reads [tool.bumpversion])" {
		t.Errorf("got %v", got.Engine)
	}
}

// TestStrattTomlBumpSection — `[bump]` at top of stratt.toml works too.
func TestStrattTomlBumpSection(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stratt.toml", "[bump]\ncurrent_version = \"1.0.0\"\n")
	touch(t, dir, "go.mod")

	r := New(dir)
	got := r.Resolve("release")
	if got.Engine == nil || got.Engine.Name() != "native bump engine (reads [tool.bumpversion])" {
		t.Errorf("got %v", got.Engine)
	}
}

const ansibleGalaxyYML = `namespace: zebpalmer
name: tailscale
version: 0.7.2
readme: README.md
`

// ansibleCollectionDir creates a temp dir containing a minimal galaxy.yml
// and roles/<role>/{tasks,meta}/main.yml.
func ansibleCollectionDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "galaxy.yml", ansibleGalaxyYML)
	touch(t, dir, "roles/machine/tasks/main.yml")
	touch(t, dir, "roles/machine/meta/main.yml")
	return dir
}

func TestBuildChainAnsibleCollection(t *testing.T) {
	r := New(ansibleCollectionDir(t))
	got := r.Resolve("build")
	if got.Engine == nil {
		t.Fatal("expected engine, got nil")
	}
	want := "ansible-galaxy collection build --force --output-path dist"
	if got.Engine.Name() != want {
		t.Errorf("got %q, want %q", got.Engine.Name(), want)
	}
}

// TestBuildChainAnsibleBeatsPythonUV — a collection that also has
// pyproject.toml + uv.lock for tooling should still build as Ansible,
// not run `uv build` on a non-Python package.
func TestBuildChainAnsibleBeatsPythonUV(t *testing.T) {
	dir := ansibleCollectionDir(t)
	touch(t, dir, "pyproject.toml")
	touch(t, dir, "uv.lock")

	r := New(dir)
	got := r.Resolve("build")
	if got.Engine == nil || got.Engine.Name() != "ansible-galaxy collection build --force --output-path dist" {
		t.Errorf("got %v", got.Engine)
	}
}

func TestLintChainAnsibleCollection(t *testing.T) {
	r := New(ansibleCollectionDir(t))
	got := r.Resolve("lint")
	if got.Engine == nil {
		t.Fatal("expected engine, got nil")
	}
	if got.Engine.Name() != "ansible-lint --fix" {
		t.Errorf("got %q", got.Engine.Name())
	}
}

func TestLintCheckAnsibleCollection(t *testing.T) {
	r := New(ansibleCollectionDir(t))
	got := r.ResolveLintCheck()
	if got == nil {
		t.Fatal("expected engine")
	}
	if got.Name() != "ansible-lint" {
		t.Errorf("got %q", got.Name())
	}
}

// TestFormatChainAnsibleUsesAnsibleLintFix — Ansible has no separable
// formatter, so format maps to `ansible-lint --fix` (the closest
// analog).  Crucially, this takes priority over python+uv's
// `ruff format` when uv is present only as a tooling layer.
func TestFormatChainAnsibleUsesAnsibleLintFix(t *testing.T) {
	dir := ansibleCollectionDir(t)
	touch(t, dir, "pyproject.toml")
	touch(t, dir, "uv.lock")

	r := New(dir)
	got := r.Resolve("format")
	if got.Engine == nil {
		t.Fatal("expected ansible-lint engine, got nil")
	}
	if got.Engine.Name() != "ansible-lint --fix" {
		t.Errorf("got %q, want %q", got.Engine.Name(), "ansible-lint --fix")
	}
}

func TestTestChainAnsibleCollection(t *testing.T) {
	r := New(ansibleCollectionDir(t))
	got := r.Resolve("test")
	if got.Engine == nil || got.Engine.Name() != "ansible-lint --strict" {
		t.Errorf("got %v", got.Engine)
	}
}

// TestSetupComposesSubmoduleInit — when .gitmodules is present, setup
// prepends `git submodule update --init --recursive` to the language
// step via a multiEngine.
func TestSetupComposesSubmoduleInit(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "go.mod")
	touch(t, dir, ".gitmodules")

	got := New(dir).Resolve("setup")
	if got.Engine == nil {
		t.Fatal("expected engine, got nil")
	}
	want := "git submodule update --init --recursive + go mod download"
	if got.Engine.Name() != want {
		t.Errorf("got %q, want %q", got.Engine.Name(), want)
	}
}

// TestSetupWithoutSubmodulesUnchanged — no .gitmodules means no
// submodule step gets composed in.
func TestSetupWithoutSubmodulesUnchanged(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "go.mod")

	got := New(dir).Resolve("setup")
	if got.Engine == nil || got.Engine.Name() != "go mod download" {
		t.Errorf("got %v", got.Engine)
	}
}

// TestSetupSubmodulesOnly — .gitmodules without a recognized language
// stack still resolves to the submodule init step alone, so a "docs
// repo with theme submodule" gets `stratt setup` support.
func TestSetupSubmodulesOnly(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, ".gitmodules")

	got := New(dir).Resolve("setup")
	if got.Engine == nil {
		t.Fatal("expected engine, got nil")
	}
	if got.Engine.Name() != "git submodule update --init --recursive" {
		t.Errorf("got %q", got.Engine.Name())
	}
}

// TestSyncComposesSubmoduleInit — sync also composes in submodule
// update so collaborators picking up a moved pin get it on `sync`.
func TestSyncComposesSubmoduleInit(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "go.mod")
	touch(t, dir, ".gitmodules")

	got := New(dir).Resolve("sync")
	if got.Engine == nil {
		t.Fatal("expected engine, got nil")
	}
	want := "git submodule update --init --recursive + go mod download"
	if got.Engine.Name() != want {
		t.Errorf("got %q, want %q", got.Engine.Name(), want)
	}
}

// TestSubmoduleStatusNoGitmodules — fast path: no .gitmodules means
// (0, 0) without shelling out.
func TestSubmoduleStatusNoGitmodules(t *testing.T) {
	declared, uninit := New(t.TempDir()).SubmoduleStatus()
	if declared != 0 || uninit != 0 {
		t.Errorf("got (%d, %d), want (0, 0)", declared, uninit)
	}
}

func TestReleaseChainAnsibleCollection(t *testing.T) {
	r := New(ansibleCollectionDir(t))
	got := r.Resolve("release")
	if got.Engine == nil {
		t.Fatal("expected engine, got nil")
	}
	want := "native bump engine (reads galaxy.yml version)"
	if got.Engine.Name() != want {
		t.Errorf("got %q, want %q", got.Engine.Name(), want)
	}
}
