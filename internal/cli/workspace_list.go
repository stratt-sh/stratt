package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/stratt-sh/stratt/internal/config"
	"github.com/stratt-sh/stratt/internal/workspace"
)

// newWorkspaceListCmd lists every repo under the workspace root with its
// path and a one-line description.  It exists mostly so AI agents (and
// humans) working in one repo can discover sibling repos in the same
// workspace — see `stratt agents context`, which points here.
func newWorkspaceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every repo under your workspace root with its path and description",
		Args:  cobra.NoArgs,
		Long: `Lists every git repository under your configured workspace root, showing
each repo's path (relative to the root) and a one-line description read
from its README.

Strictly read-only and offline: it walks the workspace tree and reads a
few lines of each README; it never touches git or the network.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWorkspaceList(cmd)
		},
	}
}

func runWorkspaceList(cmd *cobra.Command) error {
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

	fmt.Fprintf(out, "%d repo%s under %s\n\n", len(repos), plural(len(repos)), base)

	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, dir := range repos {
		label := dir
		if rel, err := filepath.Rel(base, dir); err == nil {
			label = rel
		}
		desc := repoDescription(dir)
		if desc == "" {
			desc = st.Faint("—")
		}
		fmt.Fprintf(tw, "%s\t%s\n", st.Bold(label), desc)
	}
	tw.Flush()
	return nil
}

// repoDescription returns a one-line, human-readable description of the
// repo at dir, derived from its README.  It prefers a blockquote tagline
// (the `> …` line many READMEs put under the H1) or the first prose line,
// falling back to the H1 heading text.  Returns "" when there is no
// README or nothing usable in it.
//
// This is best-effort presentation, not parsing: it reads only the head
// of the file and gives up quietly on any read error.
func repoDescription(dir string) string {
	f := openReadme(dir)
	if f == nil {
		return ""
	}
	defer f.Close()

	var heading string
	sc := bufio.NewScanner(f)
	lines := 0
	for sc.Scan() && lines < 60 {
		lines++
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
			continue
		case isBadgeLine(line):
			continue
		case strings.HasPrefix(line, "<!--"):
			continue
		case strings.HasPrefix(line, ">"):
			// Blockquote tagline — the strongest description signal.
			return truncateDesc(strings.TrimSpace(strings.TrimLeft(line, ">")))
		case strings.HasPrefix(line, "#"):
			// Record the first heading (usually the repo name) but keep
			// looking for a tagline or intro prose, which describe the repo
			// better than its name.  A *second* heading means we've reached
			// the section body without finding either — stop and use the
			// name rather than grabbing a section's contents.
			if heading != "" {
				return truncateDesc(heading)
			}
			heading = strings.TrimSpace(strings.TrimLeft(line, "#"))
			continue
		case isRuleLine(line):
			continue
		default:
			return truncateDesc(line)
		}
	}
	return truncateDesc(heading)
}

// openReadme finds a README in dir, matching the name case-insensitively
// so README.md, readme.md, and Readme.rst all work.  Returns nil if none.
func openReadme(dir string) *os.File {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(strings.ToLower(e.Name()), "readme") {
			if f, err := os.Open(filepath.Join(dir, e.Name())); err == nil {
				return f
			}
		}
	}
	return nil
}

// isBadgeLine reports whether a line is only shields/badges or images —
// noise we never want as a description.
func isBadgeLine(line string) bool {
	return strings.HasPrefix(line, "![") ||
		strings.HasPrefix(line, "[![") ||
		strings.Contains(line, "shields.io") ||
		strings.Contains(line, "badge")
}

// isRuleLine reports whether a line is a horizontal rule (---, ***, ___).
func isRuleLine(line string) bool {
	for _, r := range []string{"-", "*", "_"} {
		if strings.Trim(line, r) == "" && len(line) >= 3 {
			return true
		}
	}
	return false
}

// truncateDesc trims a description to a single tidy line.
func truncateDesc(s string) string {
	s = strings.TrimSpace(s)
	const max = 100
	if len([]rune(s)) > max {
		return string([]rune(s)[:max-1]) + "…"
	}
	return s
}
