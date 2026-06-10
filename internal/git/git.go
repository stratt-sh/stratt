// Package git wraps the small subset of git porcelain stratt invokes
// during release flows.  Shelling out to the `git` binary keeps this
// package free of git-library dependencies and makes its behavior
// trivially auditable against the user's local git config (signing,
// hooks, etc.).
//
// All functions accept a context so callers can cancel hung operations
// (e.g. a remote push that's blocked on auth).
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// Repo is bound to a working directory; all commands run inside it.
type Repo struct {
	Dir    string
	Stdout io.Writer // optional; defaults to discard
	Stderr io.Writer // optional; defaults to discard
}

// New returns a Repo for dir.
func New(dir string) *Repo {
	return &Repo{Dir: dir}
}

// IsRepo reports whether r.Dir is inside a git working tree.  Used to
// give a clear "not a git repository" message up front instead of a raw
// "exit status 128" from a later git call.
func (r *Repo) IsRepo(ctx context.Context) bool {
	out, err := r.captureOutput(ctx, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// Branch returns the current branch name.  Works even on a repo with
// no commits yet (where HEAD doesn't yet resolve to a sha) by using
// symbolic-ref instead of rev-parse.
func (r *Repo) Branch(ctx context.Context) (string, error) {
	out, err := r.captureOutput(ctx, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// IsClean reports whether the working tree has no uncommitted changes.
func (r *Repo) IsClean(ctx context.Context) (bool, error) {
	out, err := r.captureOutput(ctx, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// Add stages the given paths (relative to r.Dir).
func (r *Repo) Add(ctx context.Context, paths ...string) error {
	args := append([]string{"add", "--"}, paths...)
	return r.run(ctx, args...)
}

// Commit creates a commit with the given message.  Returns
// ErrNothingToCommit when the index is empty.  We check
// `git diff --cached --quiet` first because the "nothing to commit"
// message goes to stdout (not stderr), making it awkward to detect
// from a failed `git commit` invocation alone.
func (r *Repo) Commit(ctx context.Context, message string) error {
	staged, err := r.hasStagedChanges(ctx)
	if err != nil {
		return err
	}
	if !staged {
		return ErrNothingToCommit
	}
	return r.run(ctx, "commit", "-m", message)
}

// hasStagedChanges reports whether the index differs from HEAD.  Uses
// `git diff --cached --quiet`, which exits 0 when there are no staged
// changes, 1 when there are.
func (r *Repo) hasStagedChanges(ctx context.Context) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--quiet")
	cmd.Dir = r.Dir
	err := cmd.Run()
	if err == nil {
		return false, nil
	}
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("git diff --cached --quiet: %w", err)
}

// Tag creates an annotated tag pointing at HEAD.
func (r *Repo) Tag(ctx context.Context, name, message string) error {
	return r.run(ctx, "tag", "-a", name, "-m", message)
}

// RemoteURL returns the configured URL for the named remote (typically
// "origin").  Returns an error when the remote does not exist.
func (r *Repo) RemoteURL(ctx context.Context, remote string) (string, error) {
	out, err := r.captureOutput(ctx, "remote", "get-url", remote)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// AheadCount reports how many commits the current branch is ahead of
// its configured upstream.  hasUpstream is false when the branch has no
// tracking branch (or HEAD is unborn), in which case ahead is 0 — that
// is not treated as an error, since plenty of branches legitimately
// have no upstream.
func (r *Repo) AheadCount(ctx context.Context) (ahead int, hasUpstream bool, err error) {
	// Probe for an upstream first.  `@{u}` errors when none is set;
	// we read that as "no upstream", not a failure.
	if _, e := r.captureOutput(ctx, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); e != nil {
		return 0, false, nil
	}
	out, err := r.captureOutput(ctx, "rev-list", "--count", "@{u}..HEAD")
	if err != nil {
		return 0, false, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, true, fmt.Errorf("parse ahead count %q: %w", out, err)
	}
	return n, true, nil
}

// CommitCount returns the number of commits reachable from HEAD.  An
// unborn HEAD (a repo with no commits yet) reports 0 with no error.
func (r *Repo) CommitCount(ctx context.Context) (int, error) {
	out, err := r.captureOutput(ctx, "rev-list", "--count", "HEAD")
	if err != nil {
		// Unborn HEAD / no commits: not an error for our purposes.
		return 0, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("parse commit count %q: %w", out, err)
	}
	return n, nil
}

// Fetch refreshes remote-tracking refs from all remotes.  It changes no
// local working state — only the remote-tracking refs — so callers can
// treat it as read-only with respect to the user's work.
func (r *Repo) Fetch(ctx context.Context) error {
	return r.run(ctx, "fetch", "--quiet", "--all")
}

// PushBranch pushes the named branch to remote (typically "origin").
func (r *Repo) PushBranch(ctx context.Context, remote, branch string) error {
	return r.run(ctx, "push", remote, branch)
}

// PushTag pushes the named tag to remote.
func (r *Repo) PushTag(ctx context.Context, remote, tag string) error {
	return r.run(ctx, "push", remote, tag)
}

// TagExists reports whether the named tag exists locally.
func (r *Repo) TagExists(ctx context.Context, name string) (bool, error) {
	out, err := r.captureOutput(ctx, "tag", "-l", name)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == name, nil
}

// BranchExists reports whether the named local branch exists.  Uses
// `git show-ref` which exits 0 if found, 1 if not (no error in either
// case from our perspective).
func (r *Repo) BranchExists(ctx context.Context, name string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "show-ref", "--verify", "--quiet", "refs/heads/"+name)
	cmd.Dir = r.Dir
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git show-ref %s: %w", name, err)
}

// ErrNothingToCommit is returned by Commit when there's nothing staged.
var ErrNothingToCommit = errors.New("nothing to commit")

// captureOutput runs git with args and returns stdout.
func (r *Repo) captureOutput(ctx context.Context, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w (stderr: %s)",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// run runs git with args, streaming stdout/stderr to the Repo's
// configured writers (or discarding if unset).
func (r *Repo) run(ctx context.Context, args ...string) error {
	stdout := r.Stdout
	stderr := r.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	// Capture stderr separately so we can surface useful errors even
	// when stderr is being discarded (e.g., in tests).
	var stderrBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir
	cmd.Stdout = stdout
	cmd.Stderr = io.MultiWriter(stderr, &stderrBuf)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w (stderr: %s)",
			strings.Join(args, " "), err, strings.TrimSpace(stderrBuf.String()))
	}
	return nil
}
