package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/stratt-sh/stratt/internal/config"
)

// projectTasksHelp wraps cobra's default help func so the root command's
// help ends with a "Project tasks" section listing the public [tasks.*]
// declared in the repo's stratt.toml (or [tool.stratt] in pyproject.toml).
//
// The built-in commands are covered by the default help; project tasks are
// invoked via `stratt run <name>` and would otherwise be invisible.  We
// only augment the root command — subcommand help (`stratt build --help`)
// is left untouched.
func projectTasksHelp(defaultHelp func(*cobra.Command, []string)) func(*cobra.Command, []string) {
	return func(cmd *cobra.Command, args []string) {
		defaultHelp(cmd, args)
		if cmd.HasParent() {
			return // only the root command's help gets the section
		}
		printProjectTasks(cmd)
	}
}

// printProjectTasks renders the public tasks from the current repo's
// config.  It prints nothing — and never errors — when there's no config,
// the config is broken, or no public tasks are defined: help must stay
// usable everywhere, so a missing or invalid config is silently skipped.
func printProjectTasks(cmd *cobra.Command) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	proj, err := config.Load(cwd)
	if err != nil || proj == nil || proj.Source == "" {
		return
	}

	names := make([]string, 0, len(proj.Tasks))
	for name, t := range proj.Tasks {
		if t.Enabled {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return
	}
	sort.Strings(names)

	out := cmd.OutOrStdout()
	st := styleFrom(cmd.Context())
	fmt.Fprintln(out)
	fmt.Fprintf(out, "%s (from %s, run with `stratt run <task>`):\n",
		st.Bold("Project Tasks"), filepath.Base(proj.Source))
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, name := range names {
		desc := proj.Tasks[name].Description
		if desc == "" {
			desc = st.Faint("(no description)")
		}
		fmt.Fprintf(tw, "  %s\t%s\n", name, desc)
	}
	tw.Flush()
}
