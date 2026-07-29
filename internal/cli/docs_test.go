package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestDocsCommandSelectsMkDocs(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "mkdocs.yml")
	tool, argv, err := docsCommand(dir, "build")
	if err != nil {
		t.Fatal(err)
	}
	if tool != "mkdocs" || len(argv) != 1 || argv[0] != "build" {
		t.Errorf("got %s %v", tool, argv)
	}
	tool, argv, err = docsCommand(dir, "serve")
	if err != nil {
		t.Fatal(err)
	}
	if tool != "mkdocs" || argv[0] != "serve" {
		t.Errorf("serve: got %s %v", tool, argv)
	}
}

// TestDocsCommandMkDocsViaUV — in a python+uv project whose lock
// provides mkdocs, `stratt docs build/serve` routes through `uv run`
// (matching the capability chain) so the .venv-installed mkdocs is used.
func TestDocsCommandMkDocsViaUV(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "mkdocs.yml")
	touch(t, dir, "pyproject.toml")
	writeFile(t, dir, "uv.lock", "version = 1\n\n[[package]]\nname = \"mkdocs\"\nversion = \"1.6.1\"\n")

	for _, action := range []string{"build", "serve"} {
		tool, argv, err := docsCommand(dir, action)
		if err != nil {
			t.Fatal(err)
		}
		want := "run --all-extras --all-groups mkdocs " + action
		if tool != "uv" || strings.Join(argv, " ") != want {
			t.Errorf("%s: got %s %v, want uv %s", action, tool, argv, want)
		}
	}
}

// TestDocsCommandMkDocsUVWithoutPackage — the uv stack without mkdocs in
// the lock keeps the PATH-based invocation.
func TestDocsCommandMkDocsUVWithoutPackage(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "mkdocs.yml")
	touch(t, dir, "pyproject.toml")
	writeFile(t, dir, "uv.lock", "version = 1\n\n[[package]]\nname = \"mkdocs-material\"\nversion = \"9.5.0\"\n")

	tool, argv, err := docsCommand(dir, "build")
	if err != nil {
		t.Fatal(err)
	}
	if tool != "mkdocs" || len(argv) != 1 || argv[0] != "build" {
		t.Errorf("got %s %v, want mkdocs [build]", tool, argv)
	}
}

func TestDocsCommandSelectsSphinx(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "docs/conf.py")
	tool, argv, err := docsCommand(dir, "build")
	if err != nil {
		t.Fatal(err)
	}
	if tool != "sphinx-build" {
		t.Errorf("build: got %s", tool)
	}
	// Output must land in docs/_build/html so `stratt clean` (which
	// targets docs/_build) and the `all` suite agree on one location.
	if got := strings.Join(argv, " "); !strings.Contains(got, "docs/_build/html") {
		t.Errorf("build args should target docs/_build/html; got %q", got)
	}
	tool, argv, err = docsCommand(dir, "serve")
	if err != nil {
		t.Fatal(err)
	}
	if tool != "sphinx-autobuild" {
		t.Errorf("serve: got %s", tool)
	}
	if got := strings.Join(argv, " "); !strings.Contains(got, "docs/_build/html") {
		t.Errorf("serve args should target docs/_build/html; got %q", got)
	}
}

func TestDocsCommandErrorsWithoutToolchain(t *testing.T) {
	if _, _, err := docsCommand(t.TempDir(), "build"); err == nil {
		t.Fatal("expected error when no docs toolchain detected")
	}
}

func TestDocsCommandSelectsHugoInDocsSubdir(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "docs/hugo.toml")

	tool, argv, err := docsCommand(dir, "build")
	if err != nil {
		t.Fatal(err)
	}
	if tool != "hugo" {
		t.Errorf("tool: got %q", tool)
	}
	// argv should include --source docs and --minify in some order.
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--source docs") {
		t.Errorf("expected --source docs; got %v", argv)
	}
	if !strings.Contains(joined, "--minify") {
		t.Errorf("expected --minify; got %v", argv)
	}

	tool, argv, err = docsCommand(dir, "serve")
	if err != nil {
		t.Fatal(err)
	}
	if tool != "hugo" || argv[0] != "server" {
		t.Errorf("serve: got %s %v", tool, argv)
	}
}

// TestDocsCleanMkDocs — `stratt docs clean` for mkdocs removes site/.
func TestDocsCleanMkDocs(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "mkdocs.yml")
	siteDir := dir + "/site"
	if err := os.MkdirAll(siteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	withCwd(t, dir)

	cmd := newDocsCleanCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(siteDir); !os.IsNotExist(err) {
		t.Errorf("site/ should be removed: stat err=%v", err)
	}
}

// TestDocsCleanHugo — `stratt docs clean` for hugo removes <src>/public.
func TestDocsCleanHugo(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "docs/hugo.toml")
	publicDir := dir + "/docs/public"
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		t.Fatal(err)
	}
	withCwd(t, dir)

	cmd := newDocsCleanCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(publicDir); !os.IsNotExist(err) {
		t.Errorf("docs/public should be removed: stat err=%v", err)
	}
}

// TestDocsCleanWithoutToolchainErrors — `stratt docs clean` without
// any detected docs toolchain reports the same error as docs build/serve.
func TestDocsCleanWithoutToolchainErrors(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)
	cmd := newDocsCleanCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when no docs toolchain detected")
	}
}

func TestDocsCommandSelectsHugoAtRoot(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "hugo.toml")

	tool, argv, err := docsCommand(dir, "build")
	if err != nil {
		t.Fatal(err)
	}
	if tool != "hugo" {
		t.Errorf("tool: got %q", tool)
	}
	// No --source when Hugo lives at the repo root.
	for _, a := range argv {
		if a == "--source" {
			t.Errorf("did not expect --source when Hugo is at root; got %v", argv)
		}
	}
}

// TestDocsCommandSphinxViaUV — in a python+uv project whose lock provides
// sphinx and sphinx-autobuild, `stratt docs build/serve` routes through
// `uv run` (matching the capability chain) so the .venv-installed tools —
// the only ones that can import the project for autodoc — are used.
func TestDocsCommandSphinxViaUV(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "docs/conf.py")
	touch(t, dir, "pyproject.toml")
	writeFile(t, dir, "uv.lock", "version = 1\n\n[[package]]\nname = \"sphinx\"\nversion = \"8.1.3\"\n\n[[package]]\nname = \"sphinx-autobuild\"\nversion = \"2024.10.3\"\n")

	tool, argv, err := docsCommand(dir, "build")
	if err != nil {
		t.Fatal(err)
	}
	if want := "run --all-extras --all-groups sphinx-build -b html docs docs/_build/html"; tool != "uv" || strings.Join(argv, " ") != want {
		t.Errorf("build: got %s %v, want uv %s", tool, argv, want)
	}
	tool, argv, err = docsCommand(dir, "serve")
	if err != nil {
		t.Fatal(err)
	}
	if want := "run --all-extras --all-groups sphinx-autobuild docs docs/_build/html"; tool != "uv" || strings.Join(argv, " ") != want {
		t.Errorf("serve: got %s %v, want uv %s", tool, argv, want)
	}
}

// TestDocsCommandSphinxUVPartialLock — each tool is gated on its own
// package: a lock that provides sphinx but not sphinx-autobuild builds
// through uv run while serve falls back to PATH.
func TestDocsCommandSphinxUVPartialLock(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "docs/conf.py")
	touch(t, dir, "pyproject.toml")
	writeFile(t, dir, "uv.lock", "version = 1\n\n[[package]]\nname = \"sphinx\"\nversion = \"8.1.3\"\n")

	tool, _, err := docsCommand(dir, "build")
	if err != nil {
		t.Fatal(err)
	}
	if tool != "uv" {
		t.Errorf("build should route through uv; got %s", tool)
	}
	tool, argv, err := docsCommand(dir, "serve")
	if err != nil {
		t.Fatal(err)
	}
	if tool != "sphinx-autobuild" || strings.Join(argv, " ") != "docs docs/_build/html" {
		t.Errorf("serve without sphinx-autobuild in the lock should stay on PATH; got %s %v", tool, argv)
	}
}
