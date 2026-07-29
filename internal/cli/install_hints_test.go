package cli

import (
	"strings"
	"testing"

	"github.com/stratt-sh/stratt/internal/capability"
)

// TestInstallHintCovers — sanity check that every binary stratt
// resolves to in a default chain has an install hint registered.  This
// catches the case where someone adds a new chain entry but forgets
// the install registry.
func TestInstallHintCovers(t *testing.T) {
	// The set of tools that appear as the leading binary in any
	// default chain entry across chains.go.  If you add or rename a
	// backend, add it here.
	tools := []string{
		"hugo",
		"mkdocs",
		"sphinx-build",
		"sphinx-autobuild",
		"uv",
		"go",
		"gofmt",
		"golangci-lint",
		"goreleaser",
		"composer",
		"docker",
	}
	for _, tool := range tools {
		if InstallHint(tool) == "" {
			t.Errorf("InstallHint(%q) returned empty — add it to install_hints.go", tool)
		}
	}
}

// TestInstallHintUnknownReturnsEmpty — non-tool callers should get
// an empty string back, not a misleading suggestion.
func TestInstallHintUnknownReturnsEmpty(t *testing.T) {
	if got := InstallHint("totally-not-a-real-tool"); got != "" {
		t.Errorf("got %q for unknown tool, want empty", got)
	}
}

// TestInstallHintHugoBrew — the headline use case from the user.
func TestInstallHintHugoBrew(t *testing.T) {
	got := InstallHint("hugo")
	if !strings.Contains(got, "brew install hugo") {
		t.Errorf("hugo hint should mention brew install hugo; got %q", got)
	}
}

// TestInstallHintMkDocsInstallsTheTool — the hint must install mkdocs
// itself, pulling the theme in via --with.  The old suggestion
// (`uv tool install mkdocs-material`) always failed: mkdocs-material is
// a theme package that ships no executables, so uv refuses it.
func TestInstallHintMkDocsInstallsTheTool(t *testing.T) {
	got := InstallHint("mkdocs")
	if !strings.Contains(got, "uv tool install mkdocs --with mkdocs-material") {
		t.Errorf("mkdocs hint should install mkdocs with the theme via --with; got %q", got)
	}
	if strings.Contains(got, "install mkdocs-material ") || strings.HasSuffix(got, "install mkdocs-material") {
		t.Errorf("mkdocs hint must not suggest installing mkdocs-material as the tool; got %q", got)
	}
}

// TestInstallHintInRepoUVProject — inside a python+uv repo, a missing
// mkdocs is better fixed by making it a project dependency; other tools
// keep their generic hints.
func TestInstallHintInRepoUVProject(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "pyproject.toml")
	touch(t, dir, "uv.lock")
	r := capability.New(dir)

	if got := installHintInRepo(r, "mkdocs"); !strings.Contains(got, "dev dependencies") || !strings.Contains(got, "stratt sync") {
		t.Errorf("uv-repo mkdocs hint should point at dev deps + stratt sync; got %q", got)
	}
	// Sphinx especially: `uv tool install sphinx` is broken for autodoc
	// projects — a global sphinx can't import the project package or its
	// theme/extensions.  The hint must point at a project dependency.
	if got := installHintInRepo(r, "sphinx-build"); !strings.Contains(got, "add sphinx to the project's dev dependencies") {
		t.Errorf("uv-repo sphinx-build hint should point at dev deps; got %q", got)
	}
	if got := installHintInRepo(r, "sphinx-autobuild"); !strings.Contains(got, "add sphinx-autobuild to the project's dev dependencies") {
		t.Errorf("uv-repo sphinx-autobuild hint should point at dev deps; got %q", got)
	}
	if got := installHintInRepo(r, "hugo"); got != InstallHint("hugo") {
		t.Errorf("non-docs tools should keep the generic hint; got %q", got)
	}

	// Outside a uv repo the generic (corrected) mkdocs hint stands.
	plain := capability.New(t.TempDir())
	if got := installHintInRepo(plain, "mkdocs"); got != InstallHint("mkdocs") {
		t.Errorf("non-uv repo should get the generic mkdocs hint; got %q", got)
	}
}
