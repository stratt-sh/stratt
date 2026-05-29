package git

import (
	"context"
	"testing"
)

// remoteRepo creates a bare repo to act as a push target and returns its
// path.  Pairs with gitInit's working repo for ahead/upstream tests.
func bareRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustRun(t, dir, "git", "init", "--bare", "-q")
	return dir
}

func TestAheadCountNoUpstream(t *testing.T) {
	dir := gitInit(t)
	r := New(dir)
	writeFile(t, dir, "a.txt", "x")
	mustRun(t, dir, "git", "add", ".")
	mustRun(t, dir, "git", "commit", "-q", "-m", "first")

	ahead, hasUpstream, err := r.AheadCount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if hasUpstream {
		t.Error("fresh repo with no remote should have no upstream")
	}
	if ahead != 0 {
		t.Errorf("ahead = %d, want 0 when no upstream", ahead)
	}
}

func TestAheadCountTracksUpstream(t *testing.T) {
	ctx := context.Background()
	dir := gitInit(t)
	r := New(dir)
	remote := bareRepo(t)
	mustRun(t, dir, "git", "remote", "add", "origin", remote)

	writeFile(t, dir, "a.txt", "x")
	mustRun(t, dir, "git", "add", ".")
	mustRun(t, dir, "git", "commit", "-q", "-m", "first")
	mustRun(t, dir, "git", "push", "-q", "-u", "origin", "main")

	// In sync with upstream.
	ahead, hasUpstream, err := r.AheadCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !hasUpstream {
		t.Fatal("expected an upstream after push -u")
	}
	if ahead != 0 {
		t.Errorf("ahead = %d, want 0 right after push", ahead)
	}

	// Two local commits not yet pushed.
	writeFile(t, dir, "b.txt", "y")
	mustRun(t, dir, "git", "add", ".")
	mustRun(t, dir, "git", "commit", "-q", "-m", "second")
	writeFile(t, dir, "c.txt", "z")
	mustRun(t, dir, "git", "add", ".")
	mustRun(t, dir, "git", "commit", "-q", "-m", "third")

	ahead, hasUpstream, err = r.AheadCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !hasUpstream || ahead != 2 {
		t.Errorf("ahead = %d hasUpstream = %v, want 2 / true", ahead, hasUpstream)
	}
}

func TestCommitCount(t *testing.T) {
	ctx := context.Background()
	dir := gitInit(t)
	r := New(dir)

	// Unborn HEAD: no commits, no error.
	n, err := r.CommitCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("commit count on empty repo = %d, want 0", n)
	}

	writeFile(t, dir, "a.txt", "x")
	mustRun(t, dir, "git", "add", ".")
	mustRun(t, dir, "git", "commit", "-q", "-m", "first")

	n, err = r.CommitCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("commit count = %d, want 1", n)
	}
}
