package release

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stratt-sh/stratt/internal/bump"
)

// fakeTool writes an executable shell script named name into bin.  Tests
// prepend bin to PATH (via fakeToolPath) so the release flow picks these
// up instead of the real uv/npm.
func fakeTool(t *testing.T, bin, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeToolPath creates a bin dir for fake tools and prepends it to PATH
// for the duration of the test.  git stays reachable via the original PATH.
func fakeToolPath(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake tools are POSIX shell scripts")
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return bin
}

// fakeUV mimics the one behavior of `uv lock` these tests depend on: it
// rewrites uv.lock with the pyproject version in PEP 440-normalized form
// (1.2.3-rc.2 → 1.2.3rc2) — the divergence that makes a literal
// [[bump.files]] search template unable to cover prereleases.
const fakeUV = `set -e
v=$(sed -n 's/^version = "\(.*\)"$/\1/p' pyproject.toml | head -n1)
norm=$(printf '%s' "$v" | sed 's/-rc\./rc/')
printf '[[package]]\nname = "x"\nversion = "%s"\n' "$norm" > uv.lock
`

// fakeNPM mirrors `npm install --package-lock-only`: package-lock.json is
// rewritten with the version from package.json.
const fakeNPM = `set -e
v=$(sed -n 's/.*"version": "\([^"]*\)".*/\1/p' package.json | head -n1)
printf '{"name": "x", "version": "%s"}\n' "$v" > package-lock.json
`

// setupUVRepo builds a committed python+uv repo at version.  uv.lock holds
// the PEP 440-normalized form of version, as uv itself would write it.
// hooks, when non-empty, is spliced into [tool.bumpversion] verbatim.
func setupUVRepo(t *testing.T, version, hooks string) string {
	t.Helper()
	dir := t.TempDir()
	mustRun(t, dir, "git", "init", "--initial-branch=main", "-q")
	mustRun(t, dir, "git", "config", "user.email", "test@example.com")
	mustRun(t, dir, "git", "config", "user.name", "Test User")
	mustRun(t, dir, "git", "config", "commit.gpgsign", "false")
	mustRun(t, dir, "git", "config", "tag.gpgsign", "false")

	writeFile(t, dir, "pyproject.toml", `[project]
name = "x"
version = "`+version+`"

[tool.bumpversion]
current_version = "`+version+`"
commit = true
tag = true
`+hooks+`
[[tool.bumpversion.files]]
filename = "pyproject.toml"
search = "version = \"{current_version}\""
replace = "version = \"{new_version}\""
`)
	normalized := strings.ReplaceAll(version, "-rc.", "rc")
	writeFile(t, dir, "uv.lock", "[[package]]\nname = \"x\"\nversion = \""+normalized+"\"\n")
	mustRun(t, dir, "git", "add", "-A")
	mustRun(t, dir, "git", "commit", "-q", "-m", "initial")
	return dir
}

func uvRepoOptions(dir string, action bump.Action, stdout, stderr *bytes.Buffer) Options {
	return Options{
		CWD:           dir,
		Action:        action,
		HasAction:     true,
		CI:            true,
		Push:          false,
		SyncLockfiles: true,
		Stdin:         strings.NewReader(""),
		Stdout:        stdout,
		Stderr:        stderr,
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// TestReleaseSyncsUVLockIntoCommit — final version: `uv lock` runs after
// the bump, the updated uv.lock lands inside the release commit, and the
// tree is clean afterward.
func TestReleaseSyncsUVLockIntoCommit(t *testing.T) {
	bin := fakeToolPath(t)
	fakeTool(t, bin, "uv", fakeUV)
	dir := setupUVRepo(t, "1.2.3", "")

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), uvRepoOptions(dir, bump.Action{Kind: bump.Patch, Op: bump.OpRelease}, &stdout, &stderr))
	if err != nil {
		t.Fatalf("release failed: %v\nstderr: %s", err, stderr.String())
	}

	lock, _ := os.ReadFile(filepath.Join(dir, "uv.lock"))
	if !strings.Contains(string(lock), `version = "1.2.4"`) {
		t.Errorf("uv.lock not synced to 1.2.4:\n%s", lock)
	}
	if files := gitOut(t, dir, "show", "--name-only", "--pretty=format:", "HEAD"); !strings.Contains(files, "uv.lock") {
		t.Errorf("uv.lock missing from the release commit; committed files:\n%s", files)
	}
	if status := gitOut(t, dir, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Errorf("tree dirty after release:\n%s", status)
	}
	if !strings.Contains(stderr.String(), "uv lock") {
		t.Errorf("expected a progress line for uv lock; stderr:\n%s", stderr.String())
	}
}

// TestReleaseSyncsUVLockPrerelease — rc.1 → rc.2, the case a literal
// [[bump.files]] entry cannot express: pyproject carries the semver form
// (1.2.3-rc.2) while uv writes the PEP 440-normalized form (1.2.3rc2)
// into the lock.  The native sync just runs uv and lets it normalize.
func TestReleaseSyncsUVLockPrerelease(t *testing.T) {
	bin := fakeToolPath(t)
	fakeTool(t, bin, "uv", fakeUV)
	dir := setupUVRepo(t, "1.2.3-rc.1", "")

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), uvRepoOptions(dir, bump.Action{Op: bump.OpIterate}, &stdout, &stderr))
	if err != nil {
		t.Fatalf("release failed: %v\nstderr: %s", err, stderr.String())
	}

	body, _ := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
	if !strings.Contains(string(body), `version = "1.2.3-rc.2"`) {
		t.Errorf("pyproject should carry the semver form:\n%s", body)
	}
	lock, _ := os.ReadFile(filepath.Join(dir, "uv.lock"))
	if !strings.Contains(string(lock), `version = "1.2.3rc2"`) {
		t.Errorf("uv.lock should carry the PEP 440-normalized form:\n%s", lock)
	}
	if status := gitOut(t, dir, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Errorf("tree dirty after prerelease release:\n%s", status)
	}
}

// TestReleaseMonorepoSyncsSubprojectLockfiles — declared subprojects with
// a uv backend and an npm frontend: both lockfiles are synced and both
// land in the single release commit.
func TestReleaseMonorepoSyncsSubprojectLockfiles(t *testing.T) {
	bin := fakeToolPath(t)
	fakeTool(t, bin, "uv", fakeUV)
	fakeTool(t, bin, "npm", fakeNPM)

	dir := t.TempDir()
	mustRun(t, dir, "git", "init", "--initial-branch=main", "-q")
	mustRun(t, dir, "git", "config", "user.email", "test@example.com")
	mustRun(t, dir, "git", "config", "user.name", "Test User")
	mustRun(t, dir, "git", "config", "commit.gpgsign", "false")
	mustRun(t, dir, "git", "config", "tag.gpgsign", "false")

	writeFile(t, dir, "stratt.toml", `[bump]
current_version = "1.0.0"

[[bump.files]]
filename = "backend/pyproject.toml"
search = 'version = "{current_version}"'
replace = 'version = "{new_version}"'

[[bump.files]]
filename = "frontend/package.json"
search = '"version": "{current_version}"'
replace = '"version": "{new_version}"'
`)
	writeFile(t, dir, "backend/pyproject.toml", `[project]
name = "backend"
version = "1.0.0"
`)
	writeFile(t, dir, "backend/uv.lock", "[[package]]\nname = \"backend\"\nversion = \"1.0.0\"\n")
	writeFile(t, dir, "frontend/package.json", `{"name": "frontend", "version": "1.0.0"}`)
	writeFile(t, dir, "frontend/package-lock.json", `{"name": "frontend", "version": "1.0.0"}`)
	mustRun(t, dir, "git", "add", "-A")
	mustRun(t, dir, "git", "commit", "-q", "-m", "initial")

	var stdout, stderr bytes.Buffer
	opts := uvRepoOptions(dir, bump.Action{Kind: bump.Minor, Op: bump.OpRelease}, &stdout, &stderr)
	opts.Subprojects = []string{"backend", "frontend"}
	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("release failed: %v\nstderr: %s", err, stderr.String())
	}

	lock, _ := os.ReadFile(filepath.Join(dir, "backend", "uv.lock"))
	if !strings.Contains(string(lock), `version = "1.1.0"`) {
		t.Errorf("backend/uv.lock not synced:\n%s", lock)
	}
	npmLock, _ := os.ReadFile(filepath.Join(dir, "frontend", "package-lock.json"))
	if !strings.Contains(string(npmLock), `"version": "1.1.0"`) {
		t.Errorf("frontend/package-lock.json not synced:\n%s", npmLock)
	}
	files := gitOut(t, dir, "show", "--name-only", "--pretty=format:", "HEAD")
	for _, want := range []string{"backend/uv.lock", "frontend/package-lock.json"} {
		if !strings.Contains(files, want) {
			t.Errorf("%s missing from the release commit; committed files:\n%s", want, files)
		}
	}
	if status := gitOut(t, dir, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Errorf("tree dirty after monorepo release:\n%s", status)
	}
}

// TestReleaseLockSyncFailureAbortsBeforeCommit — a failing lock command
// (tool broken, resolver error, network) aborts the release before any
// commit or tag is created.
func TestReleaseLockSyncFailureAbortsBeforeCommit(t *testing.T) {
	bin := fakeToolPath(t)
	fakeTool(t, bin, "uv", `echo "error: resolution failed" >&2
exit 1
`)
	dir := setupUVRepo(t, "1.2.3", "")
	headBefore := gitOut(t, dir, "rev-parse", "HEAD")

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), uvRepoOptions(dir, bump.Action{Kind: bump.Patch, Op: bump.OpRelease}, &stdout, &stderr))
	if err == nil {
		t.Fatal("expected the failing lock sync to abort the release")
	}
	if !strings.Contains(err.Error(), "uv lock") || !strings.Contains(err.Error(), "resolution failed") {
		t.Errorf("error should name the command and carry its output: %v", err)
	}
	if headAfter := gitOut(t, dir, "rev-parse", "HEAD"); headAfter != headBefore {
		t.Error("a commit was created despite the lock-sync failure")
	}
	if tags := gitOut(t, dir, "tag", "-l"); strings.TrimSpace(tags) != "" {
		t.Errorf("a tag was created despite the lock-sync failure: %s", tags)
	}
}

// TestReleaseSyncLockfilesDisabled — sync_lockfiles = false restores the
// old behavior: the lock command never runs (proven by a fake uv that
// would fail), and no dead-hooks warning is emitted.
func TestReleaseSyncLockfilesDisabled(t *testing.T) {
	bin := fakeToolPath(t)
	fakeTool(t, bin, "uv", "exit 1\n")
	dir := setupUVRepo(t, "1.2.3", `pre_commit_hooks = ["uv sync", "git add uv.lock"]
`)

	var stdout, stderr bytes.Buffer
	opts := uvRepoOptions(dir, bump.Action{Kind: bump.Patch, Op: bump.OpRelease}, &stdout, &stderr)
	opts.SyncLockfiles = false
	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("release failed: %v\nstderr: %s", err, stderr.String())
	}

	lock, _ := os.ReadFile(filepath.Join(dir, "uv.lock"))
	if !strings.Contains(string(lock), `version = "1.2.3"`) {
		t.Errorf("uv.lock should be untouched with sync disabled:\n%s", lock)
	}
	if strings.Contains(stderr.String(), "pre_commit_hooks") {
		t.Errorf("no dead-hooks warning expected with sync disabled; stderr:\n%s", stderr.String())
	}
}

// TestReleaseWarnsOnDeadPreCommitHooks — a legacy [tool.bumpversion]
// carrying pre_commit_hooks gets exactly one warning that stratt does not
// execute them and syncs lockfiles natively instead.
func TestReleaseWarnsOnDeadPreCommitHooks(t *testing.T) {
	bin := fakeToolPath(t)
	fakeTool(t, bin, "uv", fakeUV)
	dir := setupUVRepo(t, "1.2.3", `pre_commit_hooks = ["uv sync", "git add uv.lock"]
`)

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), uvRepoOptions(dir, bump.Action{Kind: bump.Patch, Op: bump.OpRelease}, &stdout, &stderr))
	if err != nil {
		t.Fatalf("release failed: %v\nstderr: %s", err, stderr.String())
	}
	if got := strings.Count(stderr.String(), "pre_commit_hooks"); got != 1 {
		t.Errorf("want exactly one dead-hooks warning, got %d; stderr:\n%s", got, stderr.String())
	}
	if !strings.Contains(stderr.String(), "does not execute") {
		t.Errorf("warning should say stratt does not execute the hooks; stderr:\n%s", stderr.String())
	}
}

// TestReleaseNoCommitLeavesLockfileSyncedUncommitted — with commit
// disabled, the lockfile is still synced but left as an uncommitted
// modification alongside the bumped files.
func TestReleaseNoCommitLeavesLockfileSyncedUncommitted(t *testing.T) {
	bin := fakeToolPath(t)
	fakeTool(t, bin, "uv", fakeUV)
	dir := setupUVRepo(t, "1.2.3", "")
	headBefore := gitOut(t, dir, "rev-parse", "HEAD")

	commitFalse := false
	var stdout, stderr bytes.Buffer
	opts := uvRepoOptions(dir, bump.Action{Kind: bump.Patch, Op: bump.OpRelease}, &stdout, &stderr)
	opts.Commit = &commitFalse
	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("release failed: %v\nstderr: %s", err, stderr.String())
	}

	lock, _ := os.ReadFile(filepath.Join(dir, "uv.lock"))
	if !strings.Contains(string(lock), `version = "1.2.4"`) {
		t.Errorf("uv.lock should be synced even without a commit:\n%s", lock)
	}
	if headAfter := gitOut(t, dir, "rev-parse", "HEAD"); headAfter != headBefore {
		t.Error("HEAD moved; a commit was created despite commit=false")
	}
	status := gitOut(t, dir, "status", "--porcelain")
	for _, want := range []string{"uv.lock", "pyproject.toml"} {
		if !strings.Contains(status, want) {
			t.Errorf("%s should be left modified and uncommitted; status:\n%s", want, status)
		}
	}
}
