package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stratt-sh/stratt/internal/workspace"
)

func TestPruneEmptyParents(t *testing.T) {
	base := t.TempDir()
	// base/old/org/repo — repo just moved away, leaving the chain empty.
	deep := filepath.Join(base, "old", "org", "repo")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	// Simulate the move: remove the repo dir itself, then prune ancestors.
	if err := os.Remove(deep); err != nil {
		t.Fatal(err)
	}
	pruneEmptyParents(deep, base)

	if _, err := os.Stat(filepath.Join(base, "old")); !os.IsNotExist(err) {
		t.Errorf("expected base/old to be pruned, stat err = %v", err)
	}
	if _, err := os.Stat(base); err != nil {
		t.Errorf("base itself must never be pruned: %v", err)
	}
}

func TestPruneEmptyParentsStopsAtNonEmpty(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "org", "repo")
	sibling := filepath.Join(base, "org", "sibling")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(repo); err != nil {
		t.Fatal(err)
	}
	pruneEmptyParents(repo, base)

	// base/org still has `sibling`, so it must survive.
	if _, err := os.Stat(sibling); err != nil {
		t.Errorf("sibling should survive: %v", err)
	}
}

func TestApplyMoveRelocatesRepo(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "wrong", "place", "repo")
	if err := os.MkdirAll(filepath.Join(src, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "file.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(base, "github.com", "org", "repo")

	if err := applyMove(organizeAction{Path: src, Target: dst, Status: orgMove}, base); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".git")); err != nil {
		t.Errorf("repo not at target: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "file.txt")); err != nil {
		t.Errorf("contents not moved: %v", err)
	}
	// Old chain pruned.
	if _, err := os.Stat(filepath.Join(base, "wrong")); !os.IsNotExist(err) {
		t.Errorf("old parent chain should be pruned, err = %v", err)
	}
}

// --- planRepo integration tests (use real git) ---

func gitRepoWithOrigin(t *testing.T, dir, originURL string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if originURL != "" {
		run("remote", "add", "origin", originURL)
	}
}

func TestPlanRepoMove(t *testing.T) {
	root := t.TempDir()
	// Repo currently at root/misc/foo, origin says it belongs at
	// root/github.com/stratt-sh/stratt.
	dir := filepath.Join(root, "misc", "foo")
	gitRepoWithOrigin(t, dir, "https://github.com/stratt-sh/stratt.git")

	a := planRepo(context.Background(), dir, root, workspace.DefaultLayout)
	if a.Status != orgMove {
		t.Fatalf("status = %v, want orgMove (reason: %s)", a.Status, a.Reason)
	}
	want := filepath.Join(root, "github.com", "stratt-sh", "stratt")
	if a.Target != want {
		t.Errorf("target = %q, want %q", a.Target, want)
	}
}

func TestPlanRepoInPlace(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "github.com", "stratt-sh", "stratt")
	gitRepoWithOrigin(t, dir, "git@github.com:stratt-sh/stratt.git")

	a := planRepo(context.Background(), dir, root, workspace.DefaultLayout)
	if a.Status != orgInPlace {
		t.Errorf("status = %v, want orgInPlace (reason: %s)", a.Status, a.Reason)
	}
}

func TestPlanRepoNoOrigin(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "local-only")
	gitRepoWithOrigin(t, dir, "") // no origin remote

	a := planRepo(context.Background(), dir, root, workspace.DefaultLayout)
	if a.Status != orgSkip || a.Reason != "no origin remote" {
		t.Errorf("got status %v reason %q, want skip/no origin remote", a.Status, a.Reason)
	}
}

func TestPlanRepoTargetExists(t *testing.T) {
	root := t.TempDir()
	// A misplaced repo whose canonical target is already occupied.
	dir := filepath.Join(root, "misc", "stratt")
	gitRepoWithOrigin(t, dir, "https://github.com/stratt-sh/stratt")
	occupied := filepath.Join(root, "github.com", "stratt-sh", "stratt")
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatal(err)
	}

	a := planRepo(context.Background(), dir, root, workspace.DefaultLayout)
	if a.Status != orgSkip || a.Reason != "target already exists" {
		t.Errorf("got status %v reason %q, want skip/target already exists", a.Status, a.Reason)
	}
}
