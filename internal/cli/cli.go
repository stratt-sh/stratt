// Package cli wires the Cobra command tree.
package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
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
func Run(b BuildInfo) int {
	root := newRootCmd(b)
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 1
	}
	return 0
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
		Long: `stratt is a polyglot task runner that detects your project's stack
and provides a unified CLI for build, test, release, and deploy.

It replaces Makefiles with a single, statically-linked binary that handles
the universal targets (build/test/lint/release) plus Kustomize image bumps
for Kubernetes deploys.`,
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
	root.AddCommand(newLintCmd())
	root.AddCommand(newRunCmd())
	root.AddCommand(newReleaseCmd())
	root.AddCommand(newDeployCmd())
	root.AddCommand(newCleanCmd())
	root.AddCommand(newDocsCmd())
	root.AddCommand(newSelfCmd(b))
	root.AddCommand(newConfigCmd(b))

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

	mode := ui.ColorAuto
	usr, _ := config.LoadUser()
	if usr != nil && usr.Display != nil {
		if usr.Display.Color != "" {
			mode = ui.ParseColorMode(usr.Display.Color)
		}
		if usr.Display.Verbosity != "" && !quiet && vcount == 0 {
			level = parseVerbosityString(usr.Display.Verbosity)
		}
	}
	if colorFlag != "" {
		mode = ui.ParseColorMode(colorFlag)
	}

	style := ui.NewStyle(cmd.OutOrStdout(), cmd.ErrOrStderr(), mode, level)
	cmd.SetContext(withStyle(cmd.Context(), style))
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
		case "version", "doctor", "help", "self":
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
	update.NotifyIfBehind(os.Stderr, b.Version, strattBrewFormula)
	go update.RefreshNotifierState(cmd.Context(), update.Options{
		Repo:           strattUpstreamRepo,
		CurrentVersion: b.Version,
	})
	return nil
}
