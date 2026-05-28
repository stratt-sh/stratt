package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/zebpalmer/stratt/internal/capability"
	"github.com/zebpalmer/stratt/internal/config"
)

// printConfigStatus renders the "Project config:" section of doctor.
// Reported as a separate helper so it appears in every code path —
// including "no recognized stacks", which used to return early.
//
// Intentionally never returns an error: doctor must stay usable when
// the config is broken, so config errors are *displayed*, not raised.
func printConfigStatus(out interface{ Write([]byte) (int, error) }, cwd string, proj *config.Project, cfgErr error, version string) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Project config:")
	switch {
	case cfgErr != nil:
		fmt.Fprintf(out, "  ✗ error loading config: %v\n", cfgErr)
		fmt.Fprintln(out, "    every command except `version`, `doctor`, `help`, and `self ...` will refuse to run until this is fixed.")
	case proj == nil || proj.Source == "":
		fmt.Fprintln(out, "  ✓ no project config (stratt.toml / [tool.stratt] in pyproject.toml) — zero-config mode")
	default:
		rel, err := filepath.Rel(cwd, proj.Source)
		if err != nil {
			rel = proj.Source
		}
		fmt.Fprintf(out, "  ✓ loaded %s\n", rel)
		if proj.RequiredStratt != "" {
			fmt.Fprintf(out, "    required_stratt: %s (this binary: %s)\n", proj.RequiredStratt, version)
		}
	}
}

func newDoctorCmd(b BuildInfo) *cobra.Command {
	return &cobra.Command{
		Use:     "doctor",
		Aliases: []string{"dr"},
		Short:   "Report detected stacks, resolved command backends, and binary metadata",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			cwd, err := os.Getwd()
			if err != nil {
				return err
			}

			fmt.Fprintln(out, "stratt doctor")
			fmt.Fprintln(out, "─────────────")
			fmt.Fprintf(out, "version : %s\n", b.Version)
			fmt.Fprintf(out, "commit  : %s\n", b.Commit)
			fmt.Fprintf(out, "built   : %s\n", b.Date)
			fmt.Fprintln(out)

			// Project config health check.  Doctor is exempt from the
			// strict gate in PersistentPreRunE (so users can run it on
			// broken configs to diagnose), so we re-check here and
			// surface any error inline rather than abort.  Other commands
			// would have failed at the gate with the same message.
			proj, cfgErr := config.Load(cwd)

			fmt.Fprintf(out, "Scanning %s\n", cwd)
			resolver := capability.New(cwd)
			stacks := resolver.Stacks()
			if len(stacks) == 0 {
				fmt.Fprintln(out, "  no recognized stacks found")
				printConfigStatus(out, cwd, proj, cfgErr, b.Version)
				return nil
			}
			for _, s := range stacks {
				fmt.Fprintf(out, "  ✓ %s (via %s)\n", s.Name, s.Signal)
			}

			fmt.Fprintln(out)
			fmt.Fprintln(out, "Resolved commands:")

			// Track which tools are missing so we can surface install
			// hints below the table — keeps the table tight while still
			// giving the user actionable next steps.  Order-preserving
			// dedup via a slice + a seen-set.
			var missingTools []string
			seen := map[string]bool{}

			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			for _, res := range resolver.ResolveAll() {
				if res.Engine == nil {
					fmt.Fprintf(tw, "  %s\t→ —\t(no engine matched)\n", res.Command)
					continue
				}
				marker := ""
				switch res.Engine.Status() {
				case capability.StatusMissingTool:
					marker = "[tool not on PATH]"
					// MultiTooler wins when present so fan-out engines
					// (e.g. lint = ruff + actionlint) surface every
					// underlying tool, not just the first.
					var names []string
					if mt, ok := res.Engine.(capability.MultiTooler); ok {
						names = mt.Tools()
					} else if t, ok := res.Engine.(capability.Tooler); ok {
						if n := t.Tool(); n != "" {
							names = []string{n}
						}
					}
					for _, name := range names {
						if name != "" && !seen[name] {
							seen[name] = true
							missingTools = append(missingTools, name)
						}
					}
				case capability.StatusPending:
					marker = "[not yet implemented]"
				}
				fmt.Fprintf(tw, "  %s\t→ %s\t%s\n", res.Command, res.Engine.Name(), marker)
			}
			tw.Flush()

			if len(missingTools) > 0 {
				fmt.Fprintln(out)
				fmt.Fprintln(out, "Missing tools:")
				mtw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
				for _, t := range missingTools {
					hint := InstallHint(t)
					if hint == "" {
						hint = "(no install hint — check the tool's own docs)"
					}
					// `→` matches the separator used in the Resolved
					// commands table above for visual consistency.
					fmt.Fprintf(mtw, "  %s\t→ %s\n", t, hint)
				}
				mtw.Flush()
			}

			// Opt-in hint: actionlint silently absent.  Workflows exist
			// but the tool isn't installed, so lint won't catch action
			// YAML problems.  Not an error — actionlint is intentionally
			// gated on availability to keep CI green for repos that
			// haven't installed it — but worth surfacing once.
			if wf, tool := resolver.ActionlintAvailable(); wf && !tool {
				fmt.Fprintln(out)
				fmt.Fprintln(out, "Note: workflows under .github/workflows/ are present but `actionlint` isn't on PATH —")
				fmt.Fprintln(out, "      install it (`brew install actionlint`) and stratt will lint them as part of `stratt lint`.")
			}

			// Submodule advisory: catches the "fresh clone, theme missing"
			// failure mode (e.g. Hugo themes shipped as a git submodule)
			// before it manifests as a cryptic build error.
			if declared, uninit := resolver.SubmoduleStatus(); uninit > 0 {
				fmt.Fprintln(out)
				fmt.Fprintf(out, "Note: %d of %d git submodule(s) are not checked out —\n", uninit, declared)
				fmt.Fprintln(out, "      run `stratt setup` (or `git submodule update --init --recursive`).")
			}

			printConfigStatus(out, cwd, proj, cfgErr, b.Version)
			return nil
		},
	}
}
