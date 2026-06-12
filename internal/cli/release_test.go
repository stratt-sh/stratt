package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stratt-sh/stratt/internal/kustomize"
)

// releaseDeployRepo builds a repo that can both release (bump config) and
// deploy (an overlay), with a bare remote so pushes succeed.
func releaseDeployRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustRunInDir(t, dir, "git", "init", "--initial-branch=main", "-q")
	mustRunInDir(t, dir, "git", "config", "user.email", "test@example.com")
	mustRunInDir(t, dir, "git", "config", "user.name", "Test User")
	mustRunInDir(t, dir, "git", "config", "commit.gpgsign", "false")
	mustRunInDir(t, dir, "git", "config", "tag.gpgsign", "false")

	writeFileInDir(t, dir, "pyproject.toml", `[project]
name = "x"
version = "1.0.0"

[tool.bumpversion]
current_version = "1.0.0"
commit = true
tag = true

[[tool.bumpversion.files]]
filename = "pyproject.toml"
search = "version = \"{current_version}\""
replace = "version = \"{new_version}\""
`)
	writeOverlay(t, dir, "prod", `images:
  - name: app
    newTag: 1.0.0
`)
	mustRunInDir(t, dir, "git", "add", "-A")
	mustRunInDir(t, dir, "git", "commit", "-q", "-m", "initial")

	bare := t.TempDir()
	mustRunInDir(t, bare, "git", "init", "--bare", "-q")
	mustRunInDir(t, dir, "git", "remote", "add", "origin", bare)
	mustRunInDir(t, dir, "git", "push", "-u", "origin", "main", "-q")
	return dir
}

func writeFileInDir(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(dir+"/"+name, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestReleaseDeployChainsDeploy — `release patch --deploy prod` bumps,
// ships, then updates the overlay image tag to the new version.
func TestReleaseDeployChainsDeploy(t *testing.T) {
	dir := releaseDeployRepo(t)
	withCwd(t, dir)

	cmd := newReleaseCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"patch", "--ci", "--skip-checks", "--deploy", "prod"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("release --deploy failed: %v\n%s", err, out.String())
	}

	// Version bumped.
	py, _ := os.ReadFile(dir + "/pyproject.toml")
	if !strings.Contains(string(py), `version = "1.0.1"`) {
		t.Errorf("expected version 1.0.1, got:\n%s", py)
	}
	// Overlay updated to the new version.
	overlay, _ := os.ReadFile(kustomize.OverlayPath(dir, "prod"))
	if !strings.Contains(string(overlay), "newTag: 1.0.1") {
		t.Errorf("overlay not deployed to 1.0.1:\n%s", overlay)
	}
}

// TestReleaseDeployRejectsNoPush — --deploy can't ship a version that was
// never pushed; the combination fails fast before any git work.
func TestReleaseDeployRejectsNoPush(t *testing.T) {
	dir := releaseDeployRepo(t)
	withCwd(t, dir)

	cmd := newReleaseCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"patch", "--ci", "--deploy", "prod", "--no-push"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for --deploy with --no-push")
	}
	if !strings.Contains(err.Error(), "no-push") {
		t.Errorf("error should mention --no-push: %v", err)
	}
	// Nothing should have changed.
	py, _ := os.ReadFile(dir + "/pyproject.toml")
	if !strings.Contains(string(py), `version = "1.0.0"`) {
		t.Errorf("version should be untouched, got:\n%s", py)
	}
}

// TestReleaseDeployUnknownEnvFailsFast — a bad --deploy env errors before
// the release runs, leaving the version untouched.
func TestReleaseDeployUnknownEnvFailsFast(t *testing.T) {
	dir := releaseDeployRepo(t)
	withCwd(t, dir)

	cmd := newReleaseCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"patch", "--ci", "--skip-checks", "--deploy", "nope"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for unknown --deploy env")
	}
	if !strings.Contains(err.Error(), "no overlay") {
		t.Errorf("error should mention the missing overlay: %v", err)
	}
	py, _ := os.ReadFile(dir + "/pyproject.toml")
	if !strings.Contains(string(py), `version = "1.0.0"`) {
		t.Errorf("version should be untouched, got:\n%s", py)
	}
}
