package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/stratt-sh/stratt/internal/config"
	"github.com/stratt-sh/stratt/internal/git"
	"github.com/stratt-sh/stratt/internal/workspace"
)

func newWorkspaceOrganizeCmd() *cobra.Command {
	var apply bool
	cmd := &cobra.Command{
		Use:   "organize",
		Short: "Move repos into the canonical workspace layout",
		Args:  cobra.NoArgs,
		Long: `Relocates repositories so their on-disk path matches the layout that
` + "`stratt clone`" + ` would have used, based on each repo's origin remote.  It is
the inverse of clone: clone computes a path from a URL, organize computes
the canonical path from an already-cloned repo's origin and moves it
there if it sits somewhere else.

By default this is a dry run — it prints what would move and changes
nothing.  Pass --apply to actually move the directories.  Moving a repo
is a plain directory rename (git's internals are path-independent), and
emptied parent directories are pruned afterward.

Repos are left untouched (and reported) when they have no origin remote,
an origin that can't be parsed into host/org/repo, or a canonical target
that is already occupied.

If no [workspace] is configured yet, you'll be prompted to set the root
and layout first — the same setup `+"`stratt clone`"+` runs.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkspaceOrganize(cmd, apply)
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "actually move repositories (default is a dry run)")
	return cmd
}

func runWorkspaceOrganize(cmd *cobra.Command, apply bool) error {
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

	root := usr.Workspace.Root
	layout := usr.Workspace.Layout
	if layout == "" {
		layout = workspace.DefaultLayout
	}

	base, err := workspace.ExpandRoot(root)
	if err != nil {
		return err
	}
	repos, err := workspace.FindRepos(root)
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		fmt.Fprintf(out, "No git repositories found under %s\n", base)
		return nil
	}

	var moves, skips []organizeAction
	inPlace := 0
	for _, dir := range repos {
		a := planRepo(cmd.Context(), dir, root, layout)
		switch a.Status {
		case orgMove:
			moves = append(moves, a)
		case orgSkip:
			skips = append(skips, a)
		default:
			inPlace++
		}
	}

	rel := func(p string) string {
		if r, err := filepath.Rel(base, p); err == nil {
			return r
		}
		return p
	}

	if len(moves) == 0 {
		fmt.Fprint(out, st.Success(fmt.Sprintf("all %d repos under %s are correctly placed", len(repos), base)))
	} else {
		if apply {
			fmt.Fprintf(out, "Moving %d of %d repos:\n", len(moves), len(repos))
		} else {
			fmt.Fprintf(out, "Dry run — pass --apply to move (%d of %d repos):\n", len(moves), len(repos))
		}
		for _, m := range moves {
			fmt.Fprintf(out, "  %s  %s  %s\n", rel(m.Path), st.Faint("→"), rel(m.Target))
			if apply {
				if err := applyMove(m, base); err != nil {
					fmt.Fprint(out, st.Failure(fmt.Sprintf("    %s", err)))
				}
			}
		}
	}

	if len(skips) > 0 {
		fmt.Fprintf(out, "\nLeft in place (%d):\n", len(skips))
		for _, s := range skips {
			fmt.Fprintf(out, "  %s — %s\n", rel(s.Path), s.Reason)
		}
	}
	return nil
}

type orgStatus int

const (
	orgInPlace orgStatus = iota
	orgMove
	orgSkip
)

// organizeAction is the planned outcome for a single repo.
type organizeAction struct {
	Path   string // current absolute path
	Target string // canonical absolute path (empty when uncomputable)
	Status orgStatus
	Reason string // human-readable reason when Status is orgSkip
}

// planRepo decides what should happen to one repo, using only read-only
// git queries.  Anything that can't be cleanly relocated becomes a skip
// with a reason rather than an error, so one odd repo doesn't block the
// rest.
func planRepo(ctx context.Context, dir, root, layout string) organizeAction {
	url, err := git.New(dir).RemoteURL(ctx, "origin")
	if err != nil {
		return organizeAction{Path: dir, Status: orgSkip, Reason: "no origin remote"}
	}
	remote, err := workspace.ParseRemote(url)
	if err != nil {
		return organizeAction{Path: dir, Status: orgSkip, Reason: fmt.Sprintf("origin not parseable (%s)", url)}
	}
	target, err := workspace.Resolve(root, layout, remote)
	if err != nil {
		return organizeAction{Path: dir, Status: orgSkip, Reason: err.Error()}
	}
	if filepath.Clean(dir) == filepath.Clean(target) {
		return organizeAction{Path: dir, Target: target, Status: orgInPlace}
	}
	if _, err := os.Lstat(target); err == nil {
		return organizeAction{Path: dir, Target: target, Status: orgSkip, Reason: "target already exists"}
	}
	return organizeAction{Path: dir, Target: target, Status: orgMove}
}

// applyMove relocates a repo to its canonical path and prunes any parent
// directories the move leaves empty.
func applyMove(m organizeAction, base string) error {
	if err := os.MkdirAll(filepath.Dir(m.Target), 0o755); err != nil {
		return fmt.Errorf("create parent of %s: %w", m.Target, err)
	}
	if err := os.Rename(m.Path, m.Target); err != nil {
		if errors.Is(err, syscall.EXDEV) {
			return fmt.Errorf("%s is on a different filesystem than the target; move it manually", m.Path)
		}
		return fmt.Errorf("move %s: %w", m.Path, err)
	}
	pruneEmptyParents(m.Path, base)
	return nil
}

// pruneEmptyParents removes now-empty ancestor directories of a moved
// repo, walking up toward base but never removing base itself or
// anything outside it.
func pruneEmptyParents(moved, base string) {
	base = filepath.Clean(base)
	cur := filepath.Dir(filepath.Clean(moved))
	for {
		rel, err := filepath.Rel(base, cur)
		if err != nil || rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
			return // reached base or stepped outside it
		}
		entries, err := os.ReadDir(cur)
		if err != nil || len(entries) > 0 {
			return
		}
		if err := os.Remove(cur); err != nil {
			return
		}
		cur = filepath.Dir(cur)
	}
}
