// Package cli wires the Cobra command tree.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/stratt-sh/stratt/internal/config"
	"github.com/stratt-sh/stratt/internal/ui"
	"github.com/stratt-sh/stratt/internal/update"
	"github.com/stratt-sh/stratt/internal/version"
)

// BuildInfo carries version metadata injected at link time.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// styleKey is the unexported context key for the per-invocation
// ui.Style.  Subcommands fetch it via styleFrom(ctx).
type styleKey struct{}

func withStyle(ctx context.Context, s *ui.Style) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, styleKey{}, s)
}

// styleFrom returns the ui.Style stashed by the root PersistentPreRunE.
// Subcommands invoked outside the root (e.g., in tests) get a default
// "auto" style bound to stdin/stdout.
func styleFrom(ctx context.Context) *ui.Style {
	if ctx == nil {
		return ui.NewStyle(os.Stdout, os.Stderr, ui.ColorAuto, ui.Normal)
	}
	if s, ok := ctx.Value(styleKey{}).(*ui.Style); ok && s != nil {
		return s
	}
	return ui.NewStyle(os.Stdout, os.Stderr, ui.ColorAuto, ui.Normal)
}

// Run executes the root command and returns the exit code.
//
// The root command has SilenceErrors: true so we own the error
// presentation here.  Per R5.5: 1 = user error, 2 = system error.
// Future error types (e.g., update-available advisories) may extend
// to 3+.
//
// When stdout is a terminal we frame the whole invocation: a header rule
// naming the command above the output, and a blank line below, so output
// doesn't blend into the shell prompt or surrounding text.  This is purely
// cosmetic and interactive-only: piped or redirected output is left
// untouched so scripts see exactly what the command emits.  It lives here
// rather than in Execute so it wraps every path uniformly — subcommands,
// `--help` (which bypasses the pre-run hooks), and error output alike.
func Run(b BuildInfo) int {
	root := newRootCmd(b)
	mode := resolveColorMode(colorFlagFromArgs(os.Args[1:]))
	pad := term.IsTerminal(int(os.Stdout.Fd()))
	if pad {
		st := ui.NewStyle(os.Stdout, os.Stderr, mode, ui.Normal)
		fmt.Fprintln(os.Stdout) // blank line above the header, separating from the prompt
		printHeader(os.Stdout, st, headerLabel(root), terminalWidth())
	}
	err := root.Execute()
	if err != nil {
		// Color is gated on stderr (where the error goes), not stdout, so a
		// redirected stdout with an interactive stderr still reddens it.
		errStyle := ui.NewStyle(os.Stderr, os.Stderr, mode, ui.Normal)
		fmt.Fprint(os.Stderr, errStyle.Error(err.Error()))
	}
	if pad {
		fmt.Fprintln(os.Stdout)
	}
	if err != nil {
		return 1
	}
	return 0
}

// headerLabel is the command path shown in the interactive header, e.g.
// "stratt doctor".  Falls back to "stratt" for the bare invocation,
// `--help`, or an unrecognized command (cobra surfaces those errors
// itself).
func headerLabel(root *cobra.Command) string {
	cmd, _, err := root.Find(os.Args[1:])
	if err != nil || cmd == nil {
		return root.Name()
	}
	return cmd.CommandPath()
}

// colorFlagFromArgs extracts a --color value from raw args, before cobra
// has parsed them — the header is printed pre-Execute, so it can't read
// the bound flag yet.  Supports both `--color X` and `--color=X`.
func colorFlagFromArgs(args []string) string {
	for i, a := range args {
		if a == "--color" && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, "--color="); ok {
			return v
		}
	}
	return ""
}

// terminalWidth returns stdout's column count, clamped to a sane range so
// the header rule is neither stubby on a narrow pane nor absurdly long on
// a maximized one.  Defaults to 60 when the size can't be read.
func terminalWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		w = 60
	}
	if w > 80 {
		w = 80
	}
	return w
}

// printHeader writes the interactive header: the command label followed by
// a rule that fills the line, e.g. "── stratt doctor ─────────────".  The
// label is bold and the rule dim, so it reads as a divider rather than
// content.  width is the visible column budget; the rule fills whatever
// the label and its framing leave.
func printHeader(w io.Writer, st *ui.Style, label string, width int) {
	const lead = "── "
	prefix := lead + label + " "
	fill := width - len([]rune(prefix))
	if fill < 3 {
		fill = 3
	}
	fmt.Fprintln(w, st.Faint(lead)+st.Bold(label)+" "+st.Faint(strings.Repeat("─", fill)))
}

func newRootCmd(b BuildInfo) *cobra.Command {
	var (
		verboseCount int
		quietFlag    bool
		colorFlag    string
	)
	root := &cobra.Command{
		Use:   "stratt",
		Short: "The operations chief for your repo",
		Long: `One set of commands for every repo, whatever language it's in.

build, test, lint, release, deploy — the same commands whether the repo is
Go, Python, Node, or PHP. Stratt detects the toolchain and dispatches; you
don't think about it. It also manages release versions and bumps Kustomize
image tags, replacing the per-repo Makefile.

Agents: run ` + "`stratt agents context`" + ` for an orientation plus the resolved
command map for this repo, or ` + "`stratt doctor`" + ` for a full health report.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		// PersistentPreRunE runs before every subcommand.  We use it to
		// load project config and enforce required_stratt (R2.3.12) so
		// that no command runs unsatisfied.  `version` and `doctor` are
		// exempt because users must be able to diagnose pin issues
		// without first satisfying them.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			applyVerbosityAndColor(cmd, verboseCount, quietFlag, colorFlag)
			return runRequiredVersionCheck(cmd, b)
		},
	}

	// Global persistent flags (R5.7 / R5.4).  `-v` and `-vv` bump
	// verbosity; `-q` collapses to quiet; `--color` overrides the
	// auto-detected TTY behavior.
	root.PersistentFlags().CountVarP(&verboseCount, "verbose", "v", "verbosity: -v for verbose, -vv for debug")
	root.PersistentFlags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress non-error output")
	root.PersistentFlags().StringVar(&colorFlag, "color", "", "color mode: auto | always | never (overrides $NO_COLOR and user config)")

	root.AddCommand(
		newVersionCmd(b),
		newDoctorCmd(b),
	)

	// Register the universal subcommands (build, test, lint, format,
	// setup, sync, lock, upgrade) per §0.  Custom-shape commands
	// (release, deploy, clean, docs, self) get added separately as
	// their implementations land.
	for _, spec := range universalSpecs {
		root.AddCommand(newUniversalCmd(spec))
	}
	root.AddCommand(newInitCmd(b))
	root.AddCommand(newLintCmd())
	root.AddCommand(newRunCmd())
	root.AddCommand(newReleaseCmd())
	root.AddCommand(newDeployCmd())
	root.AddCommand(newCleanCmd())
	root.AddCommand(newDocsCmd())
	root.AddCommand(newSelfCmd(b))
	root.AddCommand(newConfigCmd(b))
	root.AddCommand(newCloneCmd())
	root.AddCommand(newWorkspaceCmd())
	root.AddCommand(newAgentsCmd(b))

	// Append a "Project tasks" section to the root help so public
	// [tasks.*] from the repo config are discoverable, not just the
	// built-in commands.  Captures the default help func first so we
	// render it, then add our section.
	root.SetHelpFunc(projectTasksHelp(root.HelpFunc()))

	return root
}

// applyVerbosityAndColor resolves the global -v/-q/--color flags
// against the user config and stashes the result in the command context
// for any subcommand that wants to render styled output.
//
// User-config layer: `[display] color = "..."` and `[display] verbosity = "..."`.
// CLI flags win over config.  $NO_COLOR overrides both per the
// no-color.org convention (already handled inside ui.NewStyle).
func applyVerbosityAndColor(cmd *cobra.Command, vcount int, quiet bool, colorFlag string) {
	level := ui.Normal
	switch {
	case quiet:
		level = ui.Quiet
	case vcount >= 2:
		level = ui.Debug
	case vcount == 1:
		level = ui.Verbose
	}

	usr, _ := config.LoadUser()
	if usr != nil && usr.Display != nil && usr.Display.Verbosity != "" && !quiet && vcount == 0 {
		level = parseVerbosityString(usr.Display.Verbosity)
	}

	style := ui.NewStyle(cmd.OutOrStdout(), cmd.ErrOrStderr(), resolveColorMode(colorFlag), level)
	cmd.SetContext(withStyle(cmd.Context(), style))
}

// resolveColorMode picks the color mode from the user config and the
// --color flag, flag winning.  $NO_COLOR is applied later inside
// ui.NewStyle.  Shared by the per-command style (applyVerbosityAndColor)
// and the interactive header in Run, so both honor the same precedence.
func resolveColorMode(colorFlag string) ui.ColorMode {
	mode := ui.ColorAuto
	if usr, _ := config.LoadUser(); usr != nil && usr.Display != nil && usr.Display.Color != "" {
		mode = ui.ParseColorMode(usr.Display.Color)
	}
	if colorFlag != "" {
		mode = ui.ParseColorMode(colorFlag)
	}
	return mode
}

func parseVerbosityString(s string) ui.Level {
	switch s {
	case "quiet":
		return ui.Quiet
	case "verbose":
		return ui.Verbose
	case "debug":
		return ui.Debug
	}
	return ui.Normal
}

// runRequiredVersionCheck loads project config, enforces
// required_stratt (R2.3.12), and (in the background) opportunistically
// pings the update notifier (R4.12).  Returns nil if either no config
// exists or the constraint passes.  Skipped for diagnostic and
// self-management commands (`version`, `doctor`, `help`, `self`) so
// users can introspect — and update — their binary regardless of the
// project config state.  Without the `self` exemption, a malformed
// stratt.toml would block `stratt self check`/`self update`, which is
// exactly when the user most needs them.
func runRequiredVersionCheck(cmd *cobra.Command, b BuildInfo) error {
	// `self` has subcommands (check, update, ...); cmd.Name() reports
	// the deepest command, so walk up to recognize the family.
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "version", "doctor", "help", "self", "clone", "agents":
			return nil
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}

	proj, err := config.Load(cwd)
	if err != nil {
		// Config errors (e.g. ErrConflict) must surface — non-skippable per R2.3.3.
		return err
	}

	if proj != nil {
		if err := version.Check(proj.RequiredStratt, b.Version); err != nil {
			return err
		}
	}

	// Deprecation scan (R2.3.9).  We render findings to stderr without
	// blocking the command.  AutoFix-eligible findings get a "run
	// stratt config migrate" hint; pure-info findings get a plain hint.
	if findings, _ := config.Scan(cwd); len(findings) > 0 {
		for _, f := range findings {
			fmt.Fprintf(os.Stderr, "[%s] %s: %s\n", f.Severity, f.ID, f.Hint)
			if f.AutoFix != nil {
				fmt.Fprintln(os.Stderr, "       run `stratt config migrate` to fix")
			}
		}
	}

	// Two-stage notifier: print cached advisory synchronously (no IO race),
	// then refresh the cache in the background for the next invocation.
	// Honor the user's release channel so prerelease users are notified
	// about RCs.  A bad config value just falls back to default here —
	// `stratt self check/update` surface the error explicitly.
	ch := update.ChannelDefault
	if usr, _ := config.LoadUser(); usr != nil && usr.Update != nil {
		if c, err := update.NormalizeChannel(usr.Update.Channel); err == nil {
			ch = c
		}
	}
	update.NotifyIfBehind(os.Stderr, b.Version, strattBrewFormula)
	go update.RefreshNotifierState(cmd.Context(), update.Options{
		Repo:           strattUpstreamRepo,
		Channel:        ch,
		CurrentVersion: b.Version,
	})
	return nil
}
