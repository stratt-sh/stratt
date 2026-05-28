package cli

import (
	"errors"
	"os"

	"github.com/spf13/cobra"
	"github.com/stratt-sh/stratt/internal/runner"
)

// newLintCmd implements `stratt lint`.  Defaults to the auto-fixing
// invocation per the project's opinionated stance ("call the tools the
// repo configured, in their fixing mode where one exists").  Pass
// `--check` to run a non-mutating gate — the equivalent of the
// Makefile template's `lint-check` target, useful for CI.
func newLintCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Run linters using the detected linter (auto-fixes by default; --check for read-only)",
		Long: `By default, ` + "`stratt lint`" + ` runs the repo's configured linter in its
fixing mode (e.g. ` + "`ruff check --fix`" + `, ` + "`golangci-lint run --fix`" + `).

Pass ` + "`--check`" + ` to skip the fix step and only report findings — useful
in CI where any change to the working tree would be undesirable.`,
		Args: cobra.NoArgs,
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

			// --check uses the check-only resolver path and runs the
			// resulting engine directly, bypassing the registry's
			// (auto-fixing) `lint` task.  All other lint behavior
			// (override/augment via [tasks.lint]) is preserved because
			// we only divert in the --check case.
			if check {
				eng := resolver.ResolveLintCheck()
				if eng == nil {
					return noEngineError("lint", resolver)
				}
				return run.RunEngine(cmd.Context(), eng, nil)
			}

			if err := run.RunTask(cmd.Context(), "lint"); err != nil {
				if errors.Is(err, runner.ErrUnknownTask) || errors.Is(err, runner.ErrNoEngine) {
					return noEngineError("lint", resolver)
				}
				return err
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "do not auto-fix; only report issues (CI-friendly read-only mode)")
	return cmd
}
