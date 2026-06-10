package release

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stratt-sh/stratt/internal/bump"
)

func mustRun(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupRepo creates a fresh git repo with a pyproject.toml,
// [tool.bumpversion] config, and an initial committed state on main.
// Push is intentionally not configured — tests pass Push: false.
func setupRepo(t *testing.T, currentVersion string) string {
	t.Helper()
	dir := t.TempDir()
	mustRun(t, dir, "git", "init", "--initial-branch=main", "-q")
	mustRun(t, dir, "git", "config", "user.email", "test@example.com")
	mustRun(t, dir, "git", "config", "user.name", "Test User")
	mustRun(t, dir, "git", "config", "commit.gpgsign", "false")
	mustRun(t, dir, "git", "config", "tag.gpgsign", "false")

	pyproject := `[project]
name = "x"
version = "` + currentVersion + `"

[tool.bumpversion]
current_version = "` + currentVersion + `"
commit = true
tag = true

[[tool.bumpversion.files]]
filename = "pyproject.toml"
search = "version = \"{current_version}\""
replace = "version = \"{new_version}\""
`
	writeFile(t, dir, "pyproject.toml", pyproject)
	mustRun(t, dir, "git", "add", "-A")
	mustRun(t, dir, "git", "commit", "-q", "-m", "initial")
	return dir
}

// TestReleaseNonInteractivePatchSuccess — the happy path with explicit
// --type=patch and --ci.  Verifies file rewrite, commit, and tag.
func TestReleaseNonInteractivePatchSuccess(t *testing.T) {
	dir := setupRepo(t, "1.2.3")
	var stdout, stderr bytes.Buffer

	err := Run(context.Background(), Options{
		CWD:       dir,
		Action:    bump.Action{Kind: bump.Patch, Op: bump.OpRelease},
		HasAction: true,
		CI:        true,
		Push:      false, // can't push in a test repo with no remote
		Stdin:     strings.NewReader(""),
		Stdout:    &stdout,
		Stderr:    &stderr,
	})
	if err != nil {
		t.Fatalf("release failed: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	// File rewrite.
	body, _ := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
	if !strings.Contains(string(body), `version = "1.2.4"`) {
		t.Errorf("pyproject not rewritten:\n%s", body)
	}

	// Tag created.
	tagOut, _ := exec.Command("git", "-C", dir, "tag", "-l").CombinedOutput()
	if !strings.Contains(string(tagOut), "v1.2.4") {
		t.Errorf("expected v1.2.4 tag, got: %s", tagOut)
	}

	// Commit created with the templated message.
	logOut, _ := exec.Command("git", "-C", dir, "log", "-1", "--pretty=%s").CombinedOutput()
	if !strings.Contains(string(logOut), "1.2.3") || !strings.Contains(string(logOut), "1.2.4") {
		t.Errorf("commit message: %s", logOut)
	}
}

// TestReleaseCommitFalseWritesButDoesNotCommit — Options.Commit=false
// (the review-then-merge flow) must rewrite the version files but make no
// commit and no tag.  Regression: the override was parsed and documented
// but never wired into release.Run.
func TestReleaseCommitFalseWritesButDoesNotCommit(t *testing.T) {
	dir := setupRepo(t, "1.2.3")
	headBefore, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	commitFalse := false
	var stdout, stderr bytes.Buffer

	err := Run(context.Background(), Options{
		CWD:       dir,
		Action:    bump.Action{Kind: bump.Patch, Op: bump.OpRelease},
		HasAction: true,
		CI:        true,
		Push:      true, // should be forced off because commit is off
		Commit:    &commitFalse,
		Stdin:     strings.NewReader(""),
		Stdout:    &stdout,
		Stderr:    &stderr,
	})
	if err != nil {
		t.Fatalf("release failed: %v\nstderr: %s", err, stderr.String())
	}

	// Files were rewritten.
	body, _ := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
	if !strings.Contains(string(body), `version = "1.2.4"`) {
		t.Errorf("pyproject not rewritten:\n%s", body)
	}
	// No new commit.
	headAfter, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if string(headBefore) != string(headAfter) {
		t.Errorf("HEAD moved; a commit was created despite commit=false")
	}
	// No tag.
	tagOut, _ := exec.Command("git", "-C", dir, "tag", "-l").CombinedOutput()
	if strings.TrimSpace(string(tagOut)) != "" {
		t.Errorf("tag created despite commit=false: %s", tagOut)
	}
}

// TestPrintPlanIsClean locks in the tidy plan rendering: the version
// transition shown once in the header, repo-relative paths, multiple
// matches in one file collapsed to a count, and none of the escaped /
// multi-line search-chunk noise the old format dumped.
func TestPrintPlanIsClean(t *testing.T) {
	root := filepath.FromSlash("/repo")
	plan := &bump.Plan{
		OldVersion:    "0.2.0",
		NewVersion:    "0.3.0",
		CommitMessage: "Bump version: 0.2.0 → 0.3.0",
		TagName:       "0.3.0",
		Cfg:           &bump.Config{Commit: true, Tag: true},
		FileChanges: []bump.FileChange{
			{Path: filepath.FromSlash("/repo/backend/pyproject.toml"), OldChunk: `version = "0.2.0"`, NewChunk: `version = "0.3.0"`, Found: true},
			{Path: filepath.FromSlash("/repo/frontend/package-lock.json"), OldChunk: "\"version\": \"0.2.0\"", NewChunk: "\"version\": \"0.3.0\"", Found: true},
			{Path: filepath.FromSlash("/repo/frontend/package-lock.json"), OldChunk: "\"version\": \"0.2.0\"", NewChunk: "\"version\": \"0.3.0\"", Found: true},
		},
	}
	var buf bytes.Buffer
	if err := printPlan(&buf, plan, root); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	t.Logf("\n%s", out)

	wants := []string{
		"Bump 0.2.0 → 0.3.0",
		filepath.FromSlash("backend/pyproject.toml"),
		filepath.FromSlash("frontend/package-lock.json") + "  (2 matches)",
		`Commit: "Bump version: 0.2.0 → 0.3.0"`,
		"Tag:    0.3.0",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("plan output missing %q:\n%s", w, out)
		}
	}
	// No absolute paths and no escaped-quote noise.
	if strings.Contains(out, filepath.FromSlash("/repo/")) {
		t.Errorf("absolute path leaked into plan:\n%s", out)
	}
	if strings.Contains(out, `\"`) {
		t.Errorf("escaped search-chunk noise leaked into plan:\n%s", out)
	}
}

func TestReleasePreflightWrongBranchFails(t *testing.T) {
	dir := setupRepo(t, "1.0.0")
	mustRun(t, dir, "git", "checkout", "-b", "feature")

	err := Run(context.Background(), Options{
		CWD:       dir,
		Action:    bump.Action{Kind: bump.Patch, Op: bump.OpRelease},
		HasAction: true,
		CI:        true,
		Branch:    "main",
		Push:      false,
		Stdin:     strings.NewReader(""),
		Stdout:    &bytes.Buffer{},
		Stderr:    &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("expected branch-check failure")
	}
	if !strings.Contains(err.Error(), "feature") || !strings.Contains(err.Error(), "main") {
		t.Errorf("error should cite both branches: %v", err)
	}
}

func TestReleasePreflightDirtyTreeFails(t *testing.T) {
	dir := setupRepo(t, "1.0.0")
	writeFile(t, dir, "dirty.txt", "uncommitted")

	err := Run(context.Background(), Options{
		CWD:       dir,
		Action:    bump.Action{Kind: bump.Patch, Op: bump.OpRelease},
		HasAction: true,
		CI:        true,
		Push:      false,
		Stdin:     strings.NewReader(""),
		Stdout:    &bytes.Buffer{},
		Stderr:    &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("expected clean-tree check to fail")
	}
	if !strings.Contains(err.Error(), "clean") {
		t.Errorf("error should mention cleanliness: %v", err)
	}
}

func TestReleaseCIRequiresKind(t *testing.T) {
	dir := setupRepo(t, "1.0.0")
	err := Run(context.Background(), Options{
		CWD:    dir,
		CI:     true,
		Push:   false,
		Stdin:  strings.NewReader(""),
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("expected --ci-without-kind error")
	}
	if !strings.Contains(err.Error(), "--ci") {
		t.Errorf("error should reference --ci: %v", err)
	}
}

// TestReleaseNonInteractiveStdinFailsClearly — no verb, not --ci, and a
// closed (non-terminal) stdin must produce actionable guidance instead of
// a bare "EOF".
func TestReleaseNonInteractiveStdinFailsClearly(t *testing.T) {
	dir := setupRepo(t, "1.0.0")
	err := Run(context.Background(), Options{
		CWD:    dir,
		Push:   false,
		Stdin:  strings.NewReader(""), // EOF immediately
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("expected a non-interactive-stdin error")
	}
	if err.Error() == "EOF" {
		t.Errorf("got bare EOF; want actionable guidance")
	}
	if !strings.Contains(err.Error(), "--ci") {
		t.Errorf("error should mention --ci or a verb: %v", err)
	}
}

func TestReleaseInteractivePromptPatch(t *testing.T) {
	dir := setupRepo(t, "1.0.0")
	// Provide "patch\n" for the word-based prompt, then "y\n" for the final confirm.
	stdin := strings.NewReader("patch\ny\n")
	var stdout, stderr bytes.Buffer

	err := Run(context.Background(), Options{
		CWD:    dir,
		Push:   false,
		Stdin:  stdin,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("release failed: %v\nstdout: %s", err, stdout.String())
	}
	body, _ := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
	if !strings.Contains(string(body), `version = "1.0.1"`) {
		t.Errorf("expected 1.0.1, got:\n%s", body)
	}
}

func TestReleaseInteractiveUserAborts(t *testing.T) {
	dir := setupRepo(t, "1.0.0")
	// Type 'patch', then 'n' to reject the final confirmation.
	stdin := strings.NewReader("patch\nn\n")
	err := Run(context.Background(), Options{
		CWD:    dir,
		Push:   false,
		Stdin:  stdin,
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("expected aborted-by-user error")
	}
	// Original file unchanged.
	body, _ := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
	if !strings.Contains(string(body), `version = "1.0.0"`) {
		t.Errorf("abort should leave file unchanged; got:\n%s", body)
	}
}

func TestReleaseMajorRequiresExtraConfirmation(t *testing.T) {
	dir := setupRepo(t, "1.0.0")
	// Type 'major', then 'n' to the major confirmation.
	stdin := strings.NewReader("major\nn\n")
	err := Run(context.Background(), Options{
		CWD:    dir,
		Push:   false,
		Stdin:  stdin,
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("expected major-bump abort")
	}
	if !strings.Contains(err.Error(), "major") {
		t.Errorf("error should reference major: %v", err)
	}
}

func TestReleaseNoBumpConfigErrors(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "git", "init", "--initial-branch=main", "-q")
	mustRun(t, dir, "git", "config", "user.email", "test@example.com")
	mustRun(t, dir, "git", "config", "user.name", "Test User")
	mustRun(t, dir, "git", "config", "commit.gpgsign", "false")
	// Make a commit so `main` resolves to a concrete ref for the
	// branch auto-detector.  No bump config though.
	writeFile(t, dir, "README.md", "x")
	mustRun(t, dir, "git", "add", "-A")
	mustRun(t, dir, "git", "commit", "-q", "-m", "initial")
	err := Run(context.Background(), Options{
		CWD:       dir,
		Action:    bump.Action{Kind: bump.Patch, Op: bump.OpRelease},
		HasAction: true,
		CI:        true,
		Push:      false,
		Stdin:     strings.NewReader(""),
		Stdout:    &bytes.Buffer{},
		Stderr:    &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("expected no-bump-config error")
	}
	if !strings.Contains(err.Error(), "bump") {
		t.Errorf("error should reference bump config: %v", err)
	}
}

// TestReleasePreReleaseCheckIsInvoked — when PreReleaseCheck is set,
// it runs after preflight and before the bump.
// TestReleaseAutoDetectsMasterBranch — a repo whose default branch is
// `master` should release without needing --branch.
func TestReleaseAutoDetectsMasterBranch(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "git", "init", "--initial-branch=master", "-q")
	mustRun(t, dir, "git", "config", "user.email", "test@example.com")
	mustRun(t, dir, "git", "config", "user.name", "Test User")
	mustRun(t, dir, "git", "config", "commit.gpgsign", "false")
	mustRun(t, dir, "git", "config", "tag.gpgsign", "false")
	writeFile(t, dir, "pyproject.toml", `[project]
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
	mustRun(t, dir, "git", "add", "-A")
	mustRun(t, dir, "git", "commit", "-q", "-m", "initial")

	var stderr bytes.Buffer
	err := Run(context.Background(), Options{
		CWD:       dir,
		Action:    bump.Action{Kind: bump.Patch, Op: bump.OpRelease},
		HasAction: true,
		CI:        true,
		Push:      false,
		// Branch deliberately left empty → triggers auto-detect.
		Stdin:  strings.NewReader(""),
		Stdout: &bytes.Buffer{},
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("release on master should auto-detect: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "auto-detected") {
		t.Errorf("expected auto-detect notice; got:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "master") {
		t.Errorf("expected `master` in the auto-detect notice; got:\n%s", stderr.String())
	}
}

// TestReleasePrefersMainOverMaster — when both branches exist (a repo
// being migrated from master to main), main wins.
func TestReleasePrefersMainOverMaster(t *testing.T) {
	dir := setupRepo(t, "1.0.0")
	mustRun(t, dir, "git", "branch", "master")
	var stderr bytes.Buffer
	err := Run(context.Background(), Options{
		CWD:       dir,
		Action:    bump.Action{Kind: bump.Patch, Op: bump.OpRelease},
		HasAction: true,
		CI:        true,
		Push:      false,
		Stdin:     strings.NewReader(""),
		Stdout:    &bytes.Buffer{},
		Stderr:    &stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "main") {
		t.Errorf("expected main to win; stderr:\n%s", stderr.String())
	}
}

// TestReleaseNoMainOrMasterFails — repos using a non-conventional
// branch name must configure it explicitly.
func TestReleaseNoMainOrMasterFails(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "git", "init", "--initial-branch=trunk", "-q")
	mustRun(t, dir, "git", "config", "user.email", "test@example.com")
	mustRun(t, dir, "git", "config", "user.name", "Test User")
	mustRun(t, dir, "git", "config", "commit.gpgsign", "false")
	writeFile(t, dir, "x", "1")
	mustRun(t, dir, "git", "add", "-A")
	mustRun(t, dir, "git", "commit", "-q", "-m", "init")

	err := Run(context.Background(), Options{
		CWD:       dir,
		Action:    bump.Action{Kind: bump.Patch, Op: bump.OpRelease},
		HasAction: true,
		CI:        true,
		Push:      false,
		Stdin:     strings.NewReader(""),
		Stdout:    &bytes.Buffer{},
		Stderr:    &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("expected error for unconventional branch")
	}
	if !strings.Contains(err.Error(), "branch") {
		t.Errorf("error should mention branch: %v", err)
	}
}

// TestReleaseExplicitBranchOverridesAutoDetect — passing Branch in
// Options bypasses auto-detection.
func TestReleaseExplicitBranchOverridesAutoDetect(t *testing.T) {
	dir := setupRepo(t, "1.0.0")
	mustRun(t, dir, "git", "checkout", "-b", "release-2024")
	err := Run(context.Background(), Options{
		CWD:       dir,
		Action:    bump.Action{Kind: bump.Patch, Op: bump.OpRelease},
		HasAction: true,
		CI:        true,
		Push:      false,
		Branch:    "release-2024",
		Stdin:     strings.NewReader(""),
		Stdout:    &bytes.Buffer{},
		Stderr:    &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("explicit branch should work: %v", err)
	}
}

func TestReleasePreReleaseCheckIsInvoked(t *testing.T) {
	dir := setupRepo(t, "1.0.0")
	called := false
	err := Run(context.Background(), Options{
		CWD:       dir,
		Action:    bump.Action{Kind: bump.Patch, Op: bump.OpRelease},
		HasAction: true,
		CI:        true,
		Push:      false,
		Stdin:     strings.NewReader(""),
		Stdout:    &bytes.Buffer{},
		Stderr:    &bytes.Buffer{},
		PreReleaseCheck: func(_ context.Context) error {
			called = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("release failed: %v", err)
	}
	if !called {
		t.Error("PreReleaseCheck was not invoked")
	}
}

// TestReleasePreReleaseCheckFailureAborts — error from PreReleaseCheck
// aborts the release; no bump happens.
func TestReleasePreReleaseCheckFailureAborts(t *testing.T) {
	dir := setupRepo(t, "1.0.0")
	wantErr := errors.New("tests failed")
	err := Run(context.Background(), Options{
		CWD:       dir,
		Action:    bump.Action{Kind: bump.Patch, Op: bump.OpRelease},
		HasAction: true,
		CI:        true,
		Push:      false,
		Stdin:     strings.NewReader(""),
		Stdout:    &bytes.Buffer{},
		Stderr:    &bytes.Buffer{},
		PreReleaseCheck: func(_ context.Context) error {
			return wantErr
		},
	})
	if err == nil {
		t.Fatal("expected error from failed pre-release check")
	}
	if !strings.Contains(err.Error(), "tests failed") {
		t.Errorf("error should propagate cause: %v", err)
	}
	// pyproject should not have been bumped.
	body, _ := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
	if !strings.Contains(string(body), `version = "1.0.0"`) {
		t.Errorf("pyproject should be unchanged; got:\n%s", body)
	}
}

// TestReleaseDetectsDirtyTreeAfterChecks — if PreReleaseCheck leaves the
// tree dirty (e.g., an autofixer rewrote files), the release aborts so
// the user can review/commit those changes first.
func TestReleaseDetectsDirtyTreeAfterChecks(t *testing.T) {
	dir := setupRepo(t, "1.0.0")
	err := Run(context.Background(), Options{
		CWD:       dir,
		Action:    bump.Action{Kind: bump.Patch, Op: bump.OpRelease},
		HasAction: true,
		CI:        true,
		Push:      false,
		Stdin:     strings.NewReader(""),
		Stdout:    &bytes.Buffer{},
		Stderr:    &bytes.Buffer{},
		PreReleaseCheck: func(_ context.Context) error {
			// Simulate an autofixer that touched a tracked file.
			return os.WriteFile(filepath.Join(dir, "pyproject.toml"),
				[]byte(`[project]
name = "x"
version = "1.0.0"
# autofixer added this comment
`), 0o644)
		},
	})
	if err == nil {
		t.Fatal("expected dirty-tree error after PreReleaseCheck")
	}
	if !strings.Contains(err.Error(), "dirty") {
		t.Errorf("error should mention dirty tree: %v", err)
	}
}

// TestReleaseSkipChecksBypassesPreReleaseCheck — --skip-checks (SkipChecks
// flag) prevents PreReleaseCheck from running, even if set.
func TestReleaseSkipChecksBypassesPreReleaseCheck(t *testing.T) {
	dir := setupRepo(t, "1.0.0")
	called := false
	err := Run(context.Background(), Options{
		CWD:        dir,
		Action:     bump.Action{Kind: bump.Patch, Op: bump.OpRelease},
		HasAction:  true,
		CI:         true,
		Push:       false,
		SkipChecks: true,
		Stdin:      strings.NewReader(""),
		Stdout:     &bytes.Buffer{},
		Stderr:     &bytes.Buffer{},
		PreReleaseCheck: func(_ context.Context) error {
			called = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("PreReleaseCheck should not be called when SkipChecks=true")
	}
}

// TestReleaseLocalOnlyPrintsPushHint — Push: false should leave the
// commit + tag locally and print the push command.
func TestReleaseLocalOnlyPrintsPushHint(t *testing.T) {
	dir := setupRepo(t, "1.0.0")
	var stdout bytes.Buffer

	err := Run(context.Background(), Options{
		CWD:       dir,
		Action:    bump.Action{Kind: bump.Patch, Op: bump.OpRelease},
		HasAction: true,
		CI:        true,
		Push:      false,
		Stdin:     strings.NewReader(""),
		Stdout:    &stdout,
		Stderr:    &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Push manually with:") {
		t.Errorf("expected push hint; got:\n%s", out)
	}
	if !strings.Contains(out, "git push origin main") {
		t.Errorf("hint should mention `git push origin main`; got:\n%s", out)
	}
}

// TestReleasePrereleaseStart — `preminor`-equivalent: start an rc.
func TestReleasePrereleaseStart(t *testing.T) {
	dir := setupRepo(t, "0.17.0")
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), Options{
		CWD:       dir,
		Push:      false,
		CI:        true,
		Action:    bump.Action{Kind: bump.Minor, Op: bump.OpStart, Label: "rc"},
		HasAction: true,
		Stdin:     strings.NewReader(""),
		Stdout:    &stdout,
		Stderr:    &stderr,
	})
	if err != nil {
		t.Fatalf("release failed: %v\nstdout: %s", err, stdout.String())
	}
	body, _ := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
	if !strings.Contains(string(body), `version = "0.18.0-rc.1"`) {
		t.Errorf("expected 0.18.0-rc.1, got:\n%s", body)
	}
}

// TestReleasePrereleasePromote — finalize an rc by dropping the suffix.
func TestReleasePrereleasePromote(t *testing.T) {
	dir := setupRepo(t, "0.18.0-rc.2")
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), Options{
		CWD:       dir,
		Push:      false,
		CI:        true,
		Action:    bump.Action{Op: bump.OpPromote},
		HasAction: true,
		Stdin:     strings.NewReader(""),
		Stdout:    &stdout,
		Stderr:    &stderr,
	})
	if err != nil {
		t.Fatalf("release failed: %v\nstdout: %s", err, stdout.String())
	}
	body, _ := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
	if !strings.Contains(string(body), `version = "0.18.0"`) {
		t.Errorf("expected promoted 0.18.0, got:\n%s", body)
	}
}

// TestReleaseStateAwareRejectsBaseBumpMidPrerelease — a plain `patch`
// while on a prerelease is rejected with guidance.
func TestReleaseStateAwareRejectsBaseBumpMidPrerelease(t *testing.T) {
	dir := setupRepo(t, "0.18.0-rc.1")
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), Options{
		CWD:       dir,
		Push:      false,
		CI:        true,
		Action:    bump.Action{Kind: bump.Patch, Op: bump.OpRelease},
		HasAction: true,
		Stdin:     strings.NewReader(""),
		Stdout:    &stdout,
		Stderr:    &stderr,
	})
	if err == nil {
		t.Fatal("expected error: base bump on a prerelease")
	}
	if !strings.Contains(err.Error(), "prerelease") {
		t.Errorf("error should mention prerelease state; got: %v", err)
	}
}
