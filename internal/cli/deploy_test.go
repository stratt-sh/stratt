package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stratt-sh/stratt/internal/kustomize"
)

func mustRunInDir(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func writeOverlay(t *testing.T, root, env, body string) {
	t.Helper()
	path := kustomize.OverlayPath(root, env)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// deployTestRepo creates a git repo with an overlay and a bare remote
// so `git push` succeeds inside the tests.  Returns the path to the
// working tree.
func deployTestRepo(t *testing.T, overlayBody string) string {
	t.Helper()
	dir := t.TempDir()
	mustRunInDir(t, dir, "git", "init", "--initial-branch=main", "-q")
	mustRunInDir(t, dir, "git", "config", "user.email", "test@example.com")
	mustRunInDir(t, dir, "git", "config", "user.name", "Test User")
	mustRunInDir(t, dir, "git", "config", "commit.gpgsign", "false")
	writeOverlay(t, dir, "prod", overlayBody)
	mustRunInDir(t, dir, "git", "add", "-A")
	mustRunInDir(t, dir, "git", "commit", "-q", "-m", "initial")

	// Bare-repo "remote" so PushBranch works without network.
	bare := t.TempDir()
	mustRunInDir(t, bare, "git", "init", "--bare", "-q")
	mustRunInDir(t, dir, "git", "remote", "add", "origin", bare)
	mustRunInDir(t, dir, "git", "push", "-u", "origin", "main", "-q")

	return dir
}

// TestDeployNoCommitOnlyEdits — --no-commit edits the file and leaves
// the working tree dirty for the user to review.
func TestDeployNoCommitOnlyEdits(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)
	writeOverlay(t, dir, "prod", `images:
  - name: app
    newTag: 1.0.0
`)

	cmd := newDeployCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"prod", "1.0.1", "--no-commit"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(kustomize.OverlayPath(dir, "prod"))
	if !strings.Contains(string(body), "newTag: 1.0.1") {
		t.Errorf("overlay not updated:\n%s", body)
	}
	if !strings.Contains(out.String(), "Edit-only mode") {
		t.Errorf("should print edit-only hint:\n%s", out.String())
	}
}

func TestDeployErrorsOnMissingEnv(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)
	cmd := newDeployCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"nonexistent", "1.0.0", "--no-commit"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing overlay")
	}
	if !strings.Contains(err.Error(), "no overlay") {
		t.Errorf("got %v", err)
	}
}

// TestDeployDirtyTreeAborts — the default commit+push path requires a
// clean tree; an untracked or modified file must abort the deploy
// before the kustomization is even touched.
func TestDeployDirtyTreeAborts(t *testing.T) {
	dir := deployTestRepo(t, `images:
  - name: app
    newTag: 1.0.0
`)
	// Add an unrelated dirty file.
	if err := os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}
	withCwd(t, dir)

	cmd := newDeployCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"prod", "1.0.1", "--yes"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected dirty-tree error")
	}
	if !strings.Contains(err.Error(), "clean") {
		t.Errorf("error should mention clean tree: %v", err)
	}
	// And the overlay should NOT have been touched.
	body, _ := os.ReadFile(kustomize.OverlayPath(dir, "prod"))
	if !strings.Contains(string(body), "newTag: 1.0.0") {
		t.Errorf("overlay should be untouched after dirty-tree abort; got:\n%s", body)
	}
}

// TestDeployDefaultCommitsAndPushes — happy path: clean tree, edit,
// commit, push to remote.  Uses --yes to skip the confirmation prompt.
func TestDeployDefaultCommitsAndPushes(t *testing.T) {
	dir := deployTestRepo(t, `images:
  - name: app
    newTag: 1.0.0
`)
	withCwd(t, dir)

	cmd := newDeployCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"prod", "1.0.1", "--yes"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("deploy failed: %v\nstdout: %s", err, out.String())
	}

	// Commit landed with our format.
	logOut, _ := exec.Command("git", "-C", dir, "log", "-1", "--pretty=%B").CombinedOutput()
	logStr := string(logOut)
	if !strings.Contains(logStr, "stratt deploy: app version 1.0.1 to prod") {
		t.Errorf("expected `stratt deploy: app version 1.0.1 to prod` in subject; got:\n%s", logStr)
	}
	if !strings.Contains(logStr, "1.0.0 → 1.0.1") {
		t.Errorf("expected tag-transition line in body; got:\n%s", logStr)
	}
	if !strings.Contains(logStr, "kustomization.yaml") {
		t.Errorf("expected overlay path in body; got:\n%s", logStr)
	}

	// Push reached the remote.
	if !strings.Contains(out.String(), "pushed to origin/main") {
		t.Errorf("expected push confirmation in output:\n%s", out.String())
	}

	// Working tree clean again (the change was committed).
	statusOut, _ := exec.Command("git", "-C", dir, "status", "--porcelain").CombinedOutput()
	if strings.TrimSpace(string(statusOut)) != "" {
		t.Errorf("tree should be clean after deploy commit; got: %q", statusOut)
	}
}

// TestDeployNoPush — --no-push commits but skips push.
func TestDeployNoPush(t *testing.T) {
	dir := deployTestRepo(t, `images:
  - name: app
    newTag: 1.0.0
`)
	withCwd(t, dir)

	cmd := newDeployCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"prod", "1.0.1", "--yes", "--no-push"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(out.String(), "pushed to") {
		t.Errorf("--no-push should not push:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Not pushed") {
		t.Errorf("expected `Not pushed` hint:\n%s", out.String())
	}

	// Commit landed locally.
	logOut, _ := exec.Command("git", "-C", dir, "log", "-1", "--pretty=%s").CombinedOutput()
	if !strings.Contains(string(logOut), "stratt deploy: app version 1.0.1 to prod") {
		t.Errorf("expected commit subject; got: %s", logOut)
	}
}

// TestDeployImageFlagForMultipleImages — --image disambiguates which
// image to bump in a multi-image overlay.
func TestDeployImageFlagForMultipleImages(t *testing.T) {
	dir := deployTestRepo(t, `images:
  - name: foo
    newTag: 1.0.0
  - name: bar
    newTag: 2.0.0
`)
	withCwd(t, dir)

	cmd := newDeployCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"prod", "2.1.0", "--image=bar", "--yes"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(kustomize.OverlayPath(dir, "prod"))
	if !strings.Contains(string(body), "newTag: 1.0.0") || !strings.Contains(string(body), "newTag: 2.1.0") {
		t.Errorf("expected foo unchanged and bar updated:\n%s", body)
	}
}

// --- `stratt deploy envs` ---

func TestDeployEnvsListsOverlays(t *testing.T) {
	dir := t.TempDir()
	writeOverlay(t, dir, "prod", `images:
  - name: myapp
    newTag: 1.14.1
`)
	writeOverlay(t, dir, "staging", `images:
  - name: myapp
    newTag: 1.14.0
  - name: sidecar
    newTag: 0.5.2
`)
	withCwd(t, dir)

	cmd := newDeployCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"envs"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("envs failed: %v", err)
	}
	body := out.String()
	for _, want := range []string{
		"prod", "staging",
		"myapp:1.14.1",
		"myapp:1.14.0",
		"sidecar:0.5.2",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in envs output; got:\n%s", want, body)
		}
	}
}

func TestDeployEnvsNoOverlaysErrors(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)
	cmd := newDeployCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"envs"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no overlays exist")
	}
}

// TestDeployEnvsHandlesUnreadableOverlay — a malformed overlay should
// not crash the listing; it appears with an error placeholder so the
// user can still see what's there.
func TestDeployEnvsHandlesUnreadableOverlay(t *testing.T) {
	dir := t.TempDir()
	writeOverlay(t, dir, "prod", `images:
  - name: ok
    newTag: 1.0.0
`)
	// "broken" env has a syntactically-invalid kustomization.yaml.
	writeOverlay(t, dir, "broken", "not: [valid: yaml: at all")
	withCwd(t, dir)

	cmd := newDeployCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"envs"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("envs should not fail on a single broken overlay: %v", err)
	}
	body := out.String()
	if !strings.Contains(body, "prod") || !strings.Contains(body, "ok:1.0.0") {
		t.Errorf("readable overlay should still show:\n%s", body)
	}
	if !strings.Contains(body, "broken") {
		t.Errorf("broken overlay should still be listed:\n%s", body)
	}
}
