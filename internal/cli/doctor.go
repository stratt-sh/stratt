package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/stratt-sh/stratt/internal/capability"
	"github.com/stratt-sh/stratt/internal/config"
	"github.com/stratt-sh/stratt/internal/runner"
	"github.com/stratt-sh/stratt/internal/ui"
)

// renderCommandRow returns the rendered "→ ..." cell and the trailing
// marker for one row of doctor's Resolved-commands table.  Looks up the
// universal command in the merged task registry first so user overrides
// from stratt.toml are surfaced; falls back to the resolver's built-in
// engine when no registry is available or the command isn't in it.
//
// Returns ("", "") when there is genuinely nothing to show (no engine,
// no user task) — caller renders the "— (no engine matched)" sentinel.
func renderCommandRow(res capability.Resolution, reg *runner.Registry) (rendered, marker string) {
	// Prefer the merged task — user overrides live there.
	if reg != nil {
		if task := reg.Lookup(res.Command); task != nil {
			switch task.Source {
			case runner.SourceOverridden, runner.SourceUser:
				return renderUserBody(task), "[project config override]"
			case runner.SourceAugmented:
				base := "—"
				if task.Engine != nil {
					base = task.Engine.Name()
				}
				return base + augmentSuffix(task), "[project config augment]"
			}
			// SourceBuiltin (or anything else): fall through to the
			// resolver-engine path below, which carries Status() for
			// the missing-tool marker.
		}
	}
	// Resolver view (the historical path).
	if res.Engine == nil {
		return "", ""
	}
	switch res.Engine.Status() {
	case capability.StatusMissingTool:
		marker = "[tool not on PATH]"
	case capability.StatusPending:
		marker = "[not yet implemented]"
	}
	return res.Engine.Name(), marker
}

// resolvedCommand is one row of the resolved-command map: the universal
// verb, the rendered backend ("" when no engine matched), the trailing
// marker, and any missing tool binaries.  Shared by `doctor` and
// `agents context` so both render the same resolution from one source.
type resolvedCommand struct {
	Command string
	Backend string   // rendered "→" body; "" means no engine matched
	Marker  string   // e.g. "[tool not on PATH]"
	Missing []string // missing tool binary names (for install hints)

	// Steps is the per-subproject breakdown for a monorepo fan-out, so the
	// renderer can put each subproject on its own line.  Empty for single-
	// stack repos and for user-overridden verbs (which render as Backend).
	Steps []capability.FanOutView
}

// resolveCommandList builds the resolved-command map for the repo.  reg
// may be nil (config/graph error) — renderCommandRow falls back to the
// resolver-only view in that case.
func resolveCommandList(resolver *capability.Resolver, reg *runner.Registry) []resolvedCommand {
	var out []resolvedCommand
	for _, res := range resolver.ResolveAll() {
		rendered, marker := renderCommandRow(res, reg)
		rc := resolvedCommand{Command: res.Command, Backend: rendered, Marker: marker}
		// Collect missing-tool names only when showing the resolver's
		// engine — user-override shell commands have no known binary.
		if marker == "[tool not on PATH]" {
			if mt, ok := res.Engine.(capability.MultiTooler); ok {
				rc.Missing = mt.Tools()
			} else if t, ok := res.Engine.(capability.Tooler); ok {
				if n := t.Tool(); n != "" {
					rc.Missing = []string{n}
				}
			}
		}
		// Per-subproject breakdown for monorepo fan-out — only when the
		// displayed row is the resolver's engine (a user override/augment
		// renders its own body, not the fan-out).
		if isResolverRow(reg, res.Command) {
			if fo, ok := res.Engine.(capability.FanOutEngine); ok {
				rc.Steps = fo.FanOut()
			}
		}
		out = append(out, rc)
	}
	return out
}

// isResolverRow reports whether a command's displayed row comes from the
// resolver's built-in engine rather than a user override/augment.  Mirrors
// renderCommandRow's source check.
func isResolverRow(reg *runner.Registry, command string) bool {
	if reg == nil {
		return true
	}
	t := reg.Lookup(command)
	if t == nil {
		return true
	}
	switch t.Source {
	case runner.SourceOverridden, runner.SourceUser, runner.SourceAugmented:
		return false
	}
	return true
}

// writeResolvedRow writes one resolved command to tw.  A fan-out (Steps
// non-empty) breaks across lines: the command + first subproject on the
// first line, each remaining subproject on its own continuation line.
//
// cmdCell is the styled first-column label; contCell is an empty cell
// carrying the SAME style overhead, so a styled (ANSI) command still lines
// up its continuation rows under tabwriter (which counts escape bytes as
// width).
func writeResolvedRow(tw *tabwriter.Writer, st *ui.Style, indent, cmdCell, contCell string, rc resolvedCommand) {
	if rc.Backend == "" {
		fmt.Fprintf(tw, "%s%s\t→ —\t%s\n", indent, cmdCell, st.Yellow("(no engine matched)"))
		return
	}
	marker := rc.Marker
	if marker != "" {
		marker = st.Faint(marker)
	}
	if len(rc.Steps) == 0 {
		fmt.Fprintf(tw, "%s%s\t→ %s\t%s\n", indent, cmdCell, rc.Backend, marker)
		return
	}
	// Pad the "dir:" labels to a common width so the bodies line up.
	w := 0
	for _, s := range rc.Steps {
		if len(s.Dir) > w {
			w = len(s.Dir)
		}
	}
	for i, s := range rc.Steps {
		label := fmt.Sprintf("%-*s", w+1, s.Dir+":")
		if i == 0 {
			fmt.Fprintf(tw, "%s%s\t→ %s %s\t%s\n", indent, cmdCell, label, s.Body, marker)
		} else {
			fmt.Fprintf(tw, "%s%s\t→ %s %s\t\n", indent, contCell, label, s.Body)
		}
	}
}

// renderUserBody renders an overridden task's body for doctor.  Shell
// runs become `sh: cmd1; cmd2`; composites become `tasks: a + b`.
func renderUserBody(t *runner.Task) string {
	if len(t.Tasks) > 0 {
		return "tasks: " + strings.Join(t.Tasks, " + ")
	}
	if len(t.Run) > 0 {
		return "sh: " + strings.Join(t.Run, "; ")
	}
	return "—"
}

// augmentSuffix builds the parenthetical for a built-in that's been
// augmented (before/after hooks added but engine body untouched).
func augmentSuffix(t *runner.Task) string {
	var parts []string
	if len(t.Before) > 0 {
		parts = append(parts, fmt.Sprintf("+ before: %d cmd(s)", len(t.Before)))
	}
	if len(t.After) > 0 {
		parts = append(parts, fmt.Sprintf("+ after: %d cmd(s)", len(t.After)))
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// printConfigStatus renders the "Project config:" section of doctor.
// Reported as a separate helper so it appears in every code path —
// including "no recognized stacks", which used to return early.
//
// Intentionally never returns an error: doctor must stay usable when
// the config is broken, so config errors are *displayed*, not raised.
func printConfigStatus(out interface{ Write([]byte) (int, error) }, st *ui.Style, cwd string, proj *config.Project, cfgErr error, version string) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, st.Bold("Project config:"))
	switch {
	case cfgErr != nil:
		fmt.Fprintf(out, "  %s error loading config: %v\n", st.Red("✗"), cfgErr)
		fmt.Fprintln(out, "    every command except `version`, `doctor`, `help`, and `self ...` will refuse to run until this is fixed.")
	case proj == nil || proj.Source == "":
		fmt.Fprintf(out, "  %s no project config (stratt.toml / [tool.stratt] in pyproject.toml) — zero-config mode\n", st.Green("✓"))
	default:
		rel, err := filepath.Rel(cwd, proj.Source)
		if err != nil {
			rel = proj.Source
		}
		fmt.Fprintf(out, "  %s loaded %s\n", st.Green("✓"), rel)
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
			st := styleFrom(cmd.Context())

			cwd, err := os.Getwd()
			if err != nil {
				return err
			}

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
			resolver := resolverFor(cwd, proj)
			stacks := resolver.Stacks()
			subprojects := resolver.Subprojects()
			if len(stacks) == 0 && len(subprojects) == 0 {
				fmt.Fprintln(out, "  no recognized stacks found")
				printConfigStatus(out, st, cwd, proj, cfgErr, b.Version)
				return nil
			}
			for _, s := range stacks {
				fmt.Fprintf(out, "  %s %s (via %s)\n", st.Green("✓"), s.Name, s.Signal)
			}
			if len(subprojects) > 0 {
				fmt.Fprintf(out, "  %s subprojects:\n", st.Bold("›"))
				for _, m := range subprojects {
					for _, s := range m.Stacks {
						fmt.Fprintf(out, "    %s %s/ → %s (via %s)\n",
							st.Green("✓"), m.Dir, s.Name, s.Signal)
					}
				}
			}

			// Build the merged task registry so we can show the
			// effective task per universal command (built-in OR user
			// override).  Registry construction can fail on a malformed
			// task graph; in that case we fall back to the resolver-only
			// view and surface the build error inline.
			var reg *runner.Registry
			var regErr error
			if cfgErr == nil {
				reg, regErr = runner.BuildRegistry(resolver, proj)
			}

			fmt.Fprintln(out)
			fmt.Fprintln(out, st.Bold("Resolved commands:"))

			// Track which tools are missing so we can surface install
			// hints below the table — keeps the table tight while still
			// giving the user actionable next steps.  Order-preserving
			// dedup via a slice + a seen-set.
			var missingTools []string
			seen := map[string]bool{}

			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			for _, rc := range resolveCommandList(resolver, reg) {
				for _, name := range rc.Missing {
					if name != "" && !seen[name] {
						seen[name] = true
						missingTools = append(missingTools, name)
					}
				}
				writeResolvedRow(tw, st, "  ", st.Bold(rc.Command), st.Bold(""), rc)
			}
			tw.Flush()

			if regErr != nil {
				fmt.Fprintln(out)
				fmt.Fprintf(out, "%s task registry could not be built (%v) —\n", st.Yellow("Note:"), regErr)
				fmt.Fprintln(out, "      the table above shows the resolver's built-in choice; runtime would error before executing.")
			}

			if len(missingTools) > 0 {
				fmt.Fprintln(out)
				fmt.Fprintln(out, st.Bold("Missing tools:"))
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
				fmt.Fprintf(out, "%s workflows under .github/workflows/ are present but `actionlint` isn't on PATH —\n", st.Yellow("Note:"))
				fmt.Fprintln(out, "      install it (`brew install actionlint`) and stratt will lint them as part of `stratt lint`.")
			}

			// Submodule advisory: catches the "fresh clone, theme missing"
			// failure mode (e.g. Hugo themes shipped as a git submodule)
			// before it manifests as a cryptic build error.
			if declared, uninit := resolver.SubmoduleStatus(); uninit > 0 {
				fmt.Fprintln(out)
				fmt.Fprintf(out, "%s %d of %d git submodule(s) are not checked out —\n", st.Yellow("Note:"), uninit, declared)
				fmt.Fprintln(out, "      run `stratt setup` (or `git submodule update --init --recursive`).")
			}

			printConfigStatus(out, st, cwd, proj, cfgErr, b.Version)
			return nil
		},
	}
}
