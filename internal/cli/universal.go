package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/stratt-sh/stratt/internal/capability"
	"github.com/stratt-sh/stratt/internal/config"
	"github.com/stratt-sh/stratt/internal/runner"
)

// universalSpec is the per-command metadata for a stratt universal command
// (build, test, lint, ...).  Each entry produces one Cobra subcommand.
//
// Per the §0 ethos, the user types the command name once.  Detection,
// resolution, and dispatch are invisible.  The body of every command is:
//
//  1. config.Load(cwd) — read user customizations (may be nil for zero-config)
//  2. capability.New(cwd) — detect stacks
//  3. runner.BuildRegistry(resolver, project) — merge built-ins with user tasks
//  4. runner.RunTask(ctx, name) — execute the resolved task graph
//
// Using BuildRegistry rather than calling the engine directly means
// user-defined overrides/augments of a built-in (e.g. `[tasks.test]
// run = "..."`) automatically apply when the user types `stratt test`.
type universalSpec struct {
	name    string
	short   string
	long    string
	aliases []string // Cobra-level aliases (e.g. "install" for "sync")
}

// universalSpecs enumerates the universal commands wired through the
// generic dispatcher.  `release`, `deploy`, `clean`, and `docs` get
// custom subcommands (positional args, multi-engine fan-out, sub-subcommands)
// and are wired separately as their implementations land.
var universalSpecs = []universalSpec{
	{name: "build", short: "Build the project using the detected toolchain"},
	{name: "test", short: "Run tests using the detected test runner"},
	// "lint" is registered separately by newLintCmd because it needs a
	// custom --check flag for CI use (mirrors the Makefile template's
	// `lint-check` target).
	{name: "format", short: "Run formatters using the detected formatter"},
	{name: "style", short: "Run formatters and linters together (format + lint)"},
	{name: "setup", short: "Perform first-time project setup"},
	// `install` is a Cobra alias for `sync` (muscle memory from the
	// Makefile template).  Wired below.
	{name: "sync", short: "Sync dependencies from the project's lockfile", aliases: []string{"install"}},
	{name: "lock", short: "Update the project's lockfile from its manifest"},
	{name: "upgrade", short: "Upgrade all dependencies to their latest compatible versions"},
	{
		name:  "all",
		short: "Run the full verification suite (everything detected)",
		long: `Run the full verification suite for the detected stacks.

Conceptual steps, in order: sync, format, lint, test, docs.  Each step
is included only when an engine resolves for it in the detected stacks
— check ` + "`stratt doctor`" + ` to see exactly what will run.

When two steps would invoke the same tool with the same fixing intent
(e.g. Ansible repos where format and lint both run ` + "`ansible-lint --fix`" + `),
the overlapping step is folded into the other so the tool runs once.
The displayed composition reflects this with a "format(via lint)"
notation, but the conceptual step is still present.`,
	},
}

// newUniversalCmd returns a Cobra command that resolves and runs the
// backend for spec.name through the task registry.
func newUniversalCmd(spec universalSpec) *cobra.Command {
	long := spec.long
	if long == "" {
		long = spec.short + ".\n\nThe backend is selected by detection (see `stratt doctor`)."
	}
	return &cobra.Command{
		Use:     spec.name,
		Aliases: spec.aliases,
		Short:   spec.short,
		Long:    long,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}

			reg, resolver, err := loadRegistry(cwd)
			if err != nil {
				return err
			}

			run := runner.New(runner.Options{
				Stdout:   cmd.OutOrStdout(),
				Stderr:   cmd.ErrOrStderr(),
				CWD:      cwd,
				Registry: reg,
			})

			if err := run.RunTask(cmd.Context(), spec.name); err != nil {
				if errors.Is(err, runner.ErrUnknownTask) || errors.Is(err, runner.ErrNoEngine) {
					return noEngineError(spec.name, resolver)
				}
				return err
			}
			return nil
		},
	}
}

// newRunCmd implements `stratt run <task>` for user-defined tasks and
// helpers (R2.6.7).  Built-in names also work — `stratt run test` is
// equivalent to `stratt test`.
func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <task>",
		Short: "Run a task defined in stratt.toml (or any built-in by name)",
		Long: `Run a task defined in stratt.toml or [tool.stratt] in pyproject.toml.

Built-in task names (build, test, lint, ...) also work — ` + "`stratt run test`" + `
is equivalent to ` + "`stratt test`" + `.

Use ` + "`stratt help`" + ` to see all available tasks.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			reg, _, err := loadRegistry(cwd)
			if err != nil {
				return err
			}
			run := runner.New(runner.Options{
				Stdout:   cmd.OutOrStdout(),
				Stderr:   cmd.ErrOrStderr(),
				CWD:      cwd,
				Registry: reg,
			})
			if err := run.RunTask(cmd.Context(), args[0]); err != nil {
				if errors.Is(err, runner.ErrUnknownTask) {
					return fmt.Errorf("no task named %q (run `stratt help` to list available tasks)", args[0])
				}
				return err
			}
			return nil
		},
	}
}

// loadRegistry is the shared "set up to run a task" path used by both
// universal subcommands and `stratt run`.  Returns the registry and the
// resolver (the latter is exposed so callers can render no-engine
// errors that list detected stacks).
func loadRegistry(cwd string) (*runner.Registry, *capability.Resolver, error) {
	proj, err := config.Load(cwd)
	if err != nil {
		return nil, nil, err
	}
	resolver := capability.New(cwd)
	reg, err := runner.BuildRegistry(resolver, proj)
	if err != nil {
		return nil, nil, err
	}
	return reg, resolver, nil
}

// noEngineError builds a friendly error for the "this repo has no
// engine for X" case, listing detected stacks so the user can adjust
// expectations or override via config.
func noEngineError(command string, r *capability.Resolver) error {
	stacks := r.Stacks()
	if len(stacks) == 0 {
		return fmt.Errorf("no engine matched for %q: no recognized stacks in this repo (run `stratt doctor` for details)", command)
	}
	names := make([]string, len(stacks))
	for i, s := range stacks {
		names[i] = s.Name
	}
	return fmt.Errorf("no engine matched for %q: detected stacks are %v but none provide this command (run `stratt doctor` for details)", command, names)
}
