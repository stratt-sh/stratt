package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/stratt-sh/stratt/internal/config"
	"github.com/stratt-sh/stratt/internal/git"
	"github.com/stratt-sh/stratt/internal/workspace"
)

// newWorkspaceCmd groups commands that operate across every repo under
// the user's configured `[workspace]` root (see `stratt clone`).
func newWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Operate across the repos under your workspace root",
		Long: `Commands that act on every git repository beneath the workspace root
configured in ~/.stratt/config.toml (the same root ` + "`stratt clone`" + ` uses).`,
	}
	cmd.AddCommand(newWorkspaceListCmd())
	cmd.AddCommand(newWorkspaceStatusCmd())
	cmd.AddCommand(newWorkspaceOrganizeCmd())
	return cmd
}

func newWorkspaceStatusCmd() *cobra.Command {
	var fetch bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report repos with uncommitted or unpushed changes",
		Args:  cobra.NoArgs,
		Long: `Scans every git repository under your configured workspace root and
reports which ones have uncommitted changes in the working tree or local
commits that are not yet on a remote.

This command is strictly read-only: it never commits, pushes, fetches
(unless you ask), or modifies any repository in any way.  It only tells
you where work is waiting so you can go commit and push it yourself.

By default "unpushed" is measured against your last-known upstream refs
with no network access.  Pass --fetch to refresh remote-tracking refs
first for an accurate count; that makes network connections but still
changes none of your local work.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkspaceStatus(cmd, fetch)
		},
	}
	cmd.Flags().BoolVar(&fetch, "fetch", false, "refresh remote-tracking refs before measuring unpushed commits")
	return cmd
}

func runWorkspaceStatus(cmd *cobra.Command, fetch bool) error {
	st := styleFrom(cmd.Context())
	out := cmd.OutOrStdout()

	usr, err := config.LoadUser()
	if err != nil {
		return err
	}
	if usr == nil || usr.Workspace == nil || usr.Workspace.Root == "" {
		ws, err := setupWorkspaceInteractive(cmd)
		if err != nil {
			return err
		}
		usr = &config.User{Workspace: ws}
	}

	base, err := workspace.ExpandRoot(usr.Workspace.Root)
	if err != nil {
		return err
	}
	repos, err := workspace.FindRepos(usr.Workspace.Root)
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		fmt.Fprintf(out, "No git repositories found under %s\n", base)
		return nil
	}

	if fetch {
		fmt.Fprint(out, st.Progress(fmt.Sprintf("fetching %d repos…", len(repos))))
	}

	var flagged []repoReport
	for _, dir := range repos {
		r := reportRepo(cmd.Context(), dir, fetch)
		if r.needsAttention() {
			flagged = append(flagged, r)
		}
	}

	if len(flagged) == 0 {
		fmt.Fprint(out, st.Success(fmt.Sprintf("all %d repos under %s are clean and pushed", len(repos), base)))
		return nil
	}

	for _, r := range flagged {
		label := r.Path
		if rel, err := filepath.Rel(base, r.Path); err == nil {
			label = rel
		}
		fmt.Fprintf(out, "%s\n", st.Bold(label))
		for _, note := range r.notes() {
			fmt.Fprintf(out, "  %s %s\n", st.Faint("·"), note)
		}
	}
	fmt.Fprintf(out, "\n%d of %d repos need attention.\n", len(flagged), len(repos))
	return nil
}

// repoReport is the read-only state of a single repo relevant to
// "is there work here that isn't safely on a remote?".
type repoReport struct {
	Path         string
	Branch       string
	Dirty        bool
	Ahead        int
	HasUpstream  bool
	LocalCommits int   // only populated when HasUpstream is false
	FetchErr     error // non-fatal: a fetch we were asked to do failed
	Err          error // fatal for this repo: status couldn't be read
}

func (r repoReport) needsAttention() bool {
	return r.Err != nil ||
		r.FetchErr != nil ||
		r.Dirty ||
		r.Ahead > 0 ||
		(!r.HasUpstream && r.LocalCommits > 0)
}

func (r repoReport) notes() []string {
	if r.Err != nil {
		return []string{"error: " + r.Err.Error()}
	}
	var notes []string
	if r.Dirty {
		notes = append(notes, "uncommitted changes")
	}
	if r.Ahead > 0 {
		notes = append(notes, fmt.Sprintf("%d unpushed commit%s on %s",
			r.Ahead, plural(r.Ahead), branchOr(r.Branch)))
	}
	if !r.HasUpstream && r.LocalCommits > 0 {
		notes = append(notes, fmt.Sprintf("no upstream — %d local commit%s on %s never pushed",
			r.LocalCommits, plural(r.LocalCommits), branchOr(r.Branch)))
	}
	if r.FetchErr != nil {
		notes = append(notes, "fetch failed (unpushed count may be stale): "+r.FetchErr.Error())
	}
	return notes
}

// reportRepo gathers a repo's status with read-only git commands.  A
// failure reading status is recorded on the report rather than aborting
// the whole scan, so one broken repo doesn't hide the others.
func reportRepo(ctx context.Context, dir string, fetch bool) repoReport {
	rep := repoReport{Path: dir}
	r := git.New(dir)

	if fetch {
		// Best-effort: an offline/auth failure shouldn't abort the scan,
		// but we surface it so the user knows the count may be stale.
		if err := r.Fetch(ctx); err != nil {
			rep.FetchErr = err
		}
	}

	clean, err := r.IsClean(ctx)
	if err != nil {
		rep.Err = err
		return rep
	}
	rep.Dirty = !clean
	rep.Branch, _ = r.Branch(ctx)

	ahead, hasUpstream, err := r.AheadCount(ctx)
	if err != nil {
		rep.Err = err
		return rep
	}
	rep.Ahead = ahead
	rep.HasUpstream = hasUpstream
	if !hasUpstream {
		rep.LocalCommits, _ = r.CommitCount(ctx)
	}
	return rep
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func branchOr(branch string) string {
	if branch == "" {
		return "(detached HEAD)"
	}
	return branch
}
