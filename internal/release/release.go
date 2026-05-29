// Package release wires the bump engine, git operations, and pre-flight
// gates into the user-facing `stratt release` flow (R2.4).
//
// The flow is:
//
//  1. Pre-flight gates (R2.4.1): on the configured branch, working tree
//     clean, lockfile in sync, optionally tests/lint pass.
//  2. Determine bump Kind: either supplied via Options.Kind (non-
//     interactive) or prompted (interactive).
//  3. Confirmation gate for Major releases.
//  4. Compute plan (dry-run; show file-by-file diff).
//  5. Final confirmation (skip with AssumeYes or in --ci mode).
//  6. Apply: write files, stage, commit, tag.
//  7. Push (default ON per R2.4.5; configurable off).
package release

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/stratt-sh/stratt/internal/bump"
	"github.com/stratt-sh/stratt/internal/git"
	"github.com/stratt-sh/stratt/internal/ui"
)

// Options drives a single release.  Zero-value Options is *not* safe —
// at minimum the IO streams must be populated.  CWD defaults to the
// process working directory.
type Options struct {
	// CWD is the repo root.  Required.
	CWD string

	// Kind selects the bump granularity.  When zero-value (Patch with
	// the zero check fall-through), the runner falls back to interactive
	// prompting in non-CI mode.  Use HasKind to distinguish "explicitly
	// patch" from "unset".
	Kind    bump.Kind
	HasKind bool

	// Branch is the release branch.  Empty triggers auto-detection
	// (prefer `main`, fall back to `master`).  Pre-flight aborts if
	// HEAD is on a different branch than the resolved value.
	Branch string

	// Push controls whether to push commit + tag to origin after the bump.
	Push bool

	// Remote is the git remote to push to.  Default "origin".
	Remote string

	// CI disables interactive prompts.  Combined with HasKind=true this
	// produces a fully non-interactive release.
	CI bool

	// AssumeYes skips final confirmation prompts (major-bump gate still requires explicit input).
	AssumeYes bool

	// Stdin / Stdout / Stderr — required.  Stdin must be a terminal-like
	// reader for the interactive prompts to work.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// PreReleaseCheck, if non-nil, is invoked after the branch+clean
	// preflight and before the version bump.  Used to run `stratt all`
	// (or whatever the project defines as its full verification suite).
	// Returning an error aborts the release.
	//
	// After this returns, the working tree is re-checked for cleanliness
	// to catch any files modified by formatters or autofixers that were
	// not subsequently committed.
	PreReleaseCheck func(ctx context.Context) error

	// SkipChecks bypasses PreReleaseCheck entirely.  For emergency
	// releases when the full check suite is broken for unrelated reasons.
	SkipChecks bool

	// Style colorizes status and success output.  Optional; when nil, Run
	// installs a no-color style so output renders as plain text.
	Style *ui.Style
}

// Run executes one release per Options.  Returns nil on success, or a
// rich error explaining which gate failed.
func Run(ctx context.Context, opts Options) error {
	if opts.CWD == "" {
		return errors.New("CWD must be set")
	}
	if opts.Remote == "" {
		opts.Remote = "origin"
	}
	if opts.Style == nil {
		opts.Style = ui.NewStyle(opts.Stdout, opts.Stderr, ui.ColorNever, ui.Normal)
	}

	// Wrap stdin once so prompts share a buffer.  Multiple bufio.Readers
	// on the same underlying io.Reader silently lose data.
	stdin := bufio.NewReader(opts.Stdin)

	repo := git.New(opts.CWD)

	// Resolve release branch: explicit setting wins, otherwise detect.
	if opts.Branch == "" {
		detected, err := detectReleaseBranch(ctx, repo)
		if err != nil {
			return err
		}
		opts.Branch = detected
		fmt.Fprint(opts.Stderr, opts.Style.Progress(fmt.Sprintf("release branch: %s (auto-detected)", opts.Branch)))
	}

	if err := preflight(ctx, repo, opts); err != nil {
		return err
	}

	// Full verification suite (`stratt all` or equivalent).  Runs before
	// the bump so that formatters/autofixers have a chance to modify
	// files; the post-check below catches any unstaged changes.
	if !opts.SkipChecks && opts.PreReleaseCheck != nil {
		fmt.Fprint(opts.Stderr, opts.Style.Progress("running pre-release checks"))
		if err := opts.PreReleaseCheck(ctx); err != nil {
			return fmt.Errorf("pre-release checks failed: %w", err)
		}
		// Re-check clean tree.  If a formatter rewrote files during the
		// checks, abort so the user can review and commit the changes
		// before retrying the release.
		clean, err := repo.IsClean(ctx)
		if err != nil {
			return fmt.Errorf("post-check git status: %w", err)
		}
		if !clean {
			return errors.New(
				"working tree is dirty after pre-release checks " +
					"(a formatter or autofixer modified files); commit those changes and retry")
		}
	}

	// Load bump configuration.
	cfg, warn, err := bump.Load(opts.CWD)
	if err != nil {
		return fmt.Errorf("loading bump config: %w", err)
	}
	if warn != "" {
		fmt.Fprintf(opts.Stderr, "warning: %s\n", warn)
	}
	if cfg == nil {
		return errors.New(
			"no version-bump configuration found; add [bump] to stratt.toml " +
				"or [tool.bumpversion] to pyproject.toml " +
				"(see R2.4.7 for the supported locations)")
	}

	// Determine kind: explicit > prompt.
	kind, err := resolveKind(opts, stdin)
	if err != nil {
		return err
	}

	// Confirmation gate for Major.
	if kind == bump.Major {
		if err := confirmMajor(opts, stdin); err != nil {
			return err
		}
	}

	// Compute and display plan.
	plan, err := bump.Compute(cfg, kind, opts.CWD)
	if err != nil {
		return fmt.Errorf("computing bump plan: %w", err)
	}
	if err := printPlan(opts.Stdout, plan); err != nil {
		return err
	}

	// Refuse to proceed if any file change is "not found".
	for _, c := range plan.FileChanges {
		if !c.Found {
			return fmt.Errorf("%w: %s (file does not contain the search string %q)",
				bump.ErrMissingVersion, c.Path, c.OldChunk)
		}
	}

	// Final confirmation (skipped with --yes or in CI).
	if !opts.AssumeYes && !opts.CI {
		if !confirm(opts, stdin, fmt.Sprintf("\nProceed with bump %s → %s?", plan.OldVersion, plan.NewVersion), true) {
			return errors.New("aborted by user")
		}
	}

	// Apply: write files.
	if err := bump.Apply(plan); err != nil {
		return fmt.Errorf("applying bump: %w", err)
	}

	// Stage and commit.
	if cfg.Commit {
		paths := make([]string, 0, len(plan.FileChanges))
		for _, c := range plan.FileChanges {
			paths = append(paths, c.Path)
		}
		if err := repo.Add(ctx, paths...); err != nil {
			return err
		}
		if err := repo.Commit(ctx, plan.CommitMessage); err != nil {
			return err
		}
	}

	// tag follows cfg.Tag regardless of push setting
	if cfg.Tag {
		if err := repo.Tag(ctx, plan.TagName, plan.CommitMessage); err != nil {
			return err
		}
	}

	if opts.Push {
		fmt.Fprint(opts.Stdout, opts.Style.Progress(fmt.Sprintf("pushing commit to %s/%s", opts.Remote, opts.Branch)))
		if err := repo.PushBranch(ctx, opts.Remote, opts.Branch); err != nil {
			return fmt.Errorf("push branch: %w", err)
		}
		fmt.Fprint(opts.Stdout, opts.Style.Success(fmt.Sprintf("pushed commit to %s/%s", opts.Remote, opts.Branch)))
		if cfg.Tag {
			fmt.Fprint(opts.Stdout, opts.Style.Progress(fmt.Sprintf("pushing tag %s", plan.TagName)))
			if err := repo.PushTag(ctx, opts.Remote, plan.TagName); err != nil {
				return fmt.Errorf("push tag: %w", err)
			}
			fmt.Fprint(opts.Stdout, opts.Style.Success(fmt.Sprintf("pushed tag %s", plan.TagName)))
		}
		fmt.Fprintf(opts.Stdout, "\n%s\n", opts.Style.Green(
			fmt.Sprintf("✓ Released %s — remote is now at %s.", plan.NewVersion, plan.TagName)))
	} else {
		fmt.Fprintf(opts.Stdout, "\nLocal release complete (push disabled).  Push manually with:\n  git push %s %s\n",
			opts.Remote, opts.Branch)
		if cfg.Tag {
			fmt.Fprintf(opts.Stdout, "  git push %s %s\n", opts.Remote, plan.TagName)
		}
	}

	return nil
}

// detectReleaseBranch picks a release branch when the caller didn't
// supply one.  Tries `main` first, then `master`, mirroring GitHub's
// default-branch evolution.  Repos that use anything else must
// configure [release] branch = "..." (or pass --branch) explicitly.
func detectReleaseBranch(ctx context.Context, repo *git.Repo) (string, error) {
	for _, candidate := range []string{"main", "master"} {
		exists, err := repo.BranchExists(ctx, candidate)
		if err != nil {
			return "", fmt.Errorf("detecting release branch: %w", err)
		}
		if exists {
			return candidate, nil
		}
	}
	return "", errors.New(
		"no `main` or `master` branch found; configure [release] branch = \"...\" in stratt.toml, " +
			"or pass --branch <name> explicitly")
}

// preflight runs R2.4.1's branch/clean checks.  Other gates (tests, lint,
// lockfile sync) will be added as their integrations mature; this is the
// minimum viable set that protects users from the worst footguns.
func preflight(ctx context.Context, repo *git.Repo, opts Options) error {
	branch, err := repo.Branch(ctx)
	if err != nil {
		return fmt.Errorf("preflight: %w", err)
	}
	if branch != opts.Branch {
		return fmt.Errorf("preflight: on branch %q, expected %q (use a different `branch` in [release] config if intentional)",
			branch, opts.Branch)
	}
	clean, err := repo.IsClean(ctx)
	if err != nil {
		return fmt.Errorf("preflight: %w", err)
	}
	if !clean {
		return errors.New("preflight: working tree is not clean (commit or stash changes before releasing)")
	}
	return nil
}

// resolveKind picks the bump granularity.  Explicit (Options.HasKind)
// wins; otherwise prompt the user, or fail in CI mode.
func resolveKind(opts Options, stdin *bufio.Reader) (bump.Kind, error) {
	if opts.HasKind {
		return opts.Kind, nil
	}
	if opts.CI {
		return 0, errors.New("--ci requires --type=patch|minor|major (no interactive prompts)")
	}
	fmt.Fprintln(opts.Stdout, "Release type: [p]atch  [m]inor  [M]ajor")
	fmt.Fprint(opts.Stdout, "Choose: ")
	line, err := stdin.ReadString('\n')
	if err != nil {
		return 0, err
	}
	trimmed := strings.TrimSpace(line)
	// Single-char forms: case-sensitive to disambiguate 'm'/'M'.
	switch trimmed {
	case "p":
		return bump.Patch, nil
	case "m":
		return bump.Minor, nil
	case "M":
		return bump.Major, nil
	}
	// Long forms: case-insensitive.
	switch strings.ToLower(trimmed) {
	case "patch":
		return bump.Patch, nil
	case "minor":
		return bump.Minor, nil
	case "major":
		return bump.Major, nil
	}
	return 0, fmt.Errorf("invalid choice %q", trimmed)
}

// confirmMajor enforces the explicit confirmation gate for Major bumps
// per R2.4.2.4.  In CI mode, the gate is satisfied implicitly by the
// user having typed --type=major.
func confirmMajor(opts Options, stdin *bufio.Reader) error {
	if opts.CI {
		return nil
	}
	if !confirm(opts, stdin, "MAJOR release.  This is a breaking-change bump.  Are you sure?", false) {
		return errors.New("major release aborted")
	}
	return nil
}

// confirm prompts the user with a yes/no question.  defaultYes selects
// the default when the user presses enter alone.
func confirm(opts Options, stdin *bufio.Reader, prompt string, defaultYes bool) bool {
	choices := " [Y/n] "
	if !defaultYes {
		choices = " [y/N] "
	}
	fmt.Fprint(opts.Stdout, prompt+choices)
	line, err := stdin.ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return defaultYes
	case "y", "yes":
		return true
	default:
		return false
	}
}

// printPlan renders a dry-run preview to w.
func printPlan(w io.Writer, p *bump.Plan) error {
	fmt.Fprintf(w, "\nBump plan (%s → %s):\n", p.OldVersion, p.NewVersion)
	for _, c := range p.FileChanges {
		fmt.Fprintln(w, c.PreviewLine())
	}
	if p.Cfg.Commit {
		fmt.Fprintf(w, "Commit message: %q\n", p.CommitMessage)
	} else {
		fmt.Fprintln(w, "Commit: disabled in config")
	}
	if p.Cfg.Tag {
		fmt.Fprintf(w, "Tag:            %s\n", p.TagName)
	} else {
		fmt.Fprintln(w, "Tag: disabled in config")
	}
	return nil
}
