package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/stratt-sh/stratt/internal/capability"
	"github.com/stratt-sh/stratt/internal/runner"
)

// newCleanCmd implements `stratt clean`.  The removal work lives in
// capability's clean engine so it participates in the task registry like
// every other built-in ([tasks.clean] overrides/augments apply, and the
// `reset` composite can chain it); this command is the flag surface.
func newCleanCmd() *cobra.Command {
	var docker bool
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Remove build / cache artifacts for the detected stacks",
		Long: `Remove conventional build and cache artifacts for every detected stack.

Always removes:
  .stratt/cache/

Per stack:
  go                 → ./bin
  python+uv          → .venv/, build/, dist/, *.egg-info, .pytest_cache/,
                       .ruff_cache/, .coverage, htmlcov/, **/__pycache__, and
                       ` + "`uv cache clean`" + ` to drop the global uv download cache
  ansible-collection → dist/, .ansible/, collections/
  ansible-role       → .ansible/
  ansible-playbook   → .ansible/, collections/
  mkdocs             → site/
  sphinx             → docs/_build/, docs/_autosummary/
  hugo               → <hugo source>/public/

Does not touch Docker images by default; pass --docker to also prune
dangling images (repos with a docker stack).
After cleaning a python+uv repo, run ` + "`stratt setup`" + ` to rebuild .venv —
or use ` + "`stratt reset`" + `, which runs clean + setup in one step.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRegistryTaskWithClean(cmd, "clean", docker)
		},
	}
	cmd.Flags().BoolVar(&docker, "docker", false, "also prune dangling docker images (repos with a docker stack)")
	return cmd
}

// runRegistryTaskWithClean is the shared body of `stratt clean` and
// `stratt reset`: load the registry, plumb the per-invocation options
// (--docker, the command's output writer) into the built-in clean engine,
// and run the named task — so [tasks.clean]/[tasks.reset] overrides,
// augments, and disables all apply exactly as for other built-ins.
func runRegistryTaskWithClean(cmd *cobra.Command, task string, docker bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	reg, resolver, err := loadRegistry(cwd)
	if err != nil {
		return err
	}

	if !configureCleanEngine(reg, cmd.OutOrStdout(), docker) && docker {
		fmt.Fprintln(cmd.ErrOrStderr(),
			"note: --docker has no effect — project config replaces or disables the built-in clean")
	}

	run := runner.New(runner.Options{
		Stdout:   cmd.OutOrStdout(),
		Stderr:   cmd.ErrOrStderr(),
		CWD:      cwd,
		Registry: reg,
		Style:    styleFrom(cmd.Context()),
	})
	if err := run.RunTask(cmd.Context(), task); err != nil {
		if errors.Is(err, runner.ErrUnknownTask) || errors.Is(err, runner.ErrNoEngine) {
			return noEngineError(task, resolver)
		}
		return err
	}
	return nil
}

// configureCleanEngine plumbs the invocation's output writer and --docker
// toggle into the registry-held clean engine.  Returns false when the
// built-in engine isn't in play (user override via [tasks.clean], or the
// task was disabled) so the caller can surface an ignored --docker.
func configureCleanEngine(reg *runner.Registry, out io.Writer, docker bool) bool {
	t := reg.Lookup("clean")
	if t == nil || t.Engine == nil {
		return false
	}
	ce, ok := t.Engine.(capability.CleanEngine)
	if !ok {
		return false
	}
	ce.SetOutput(out)
	ce.SetDockerPrune(docker)
	return true
}

// relTo renders path relative to base for display, falling back to the
// absolute path when no relative form exists.
func relTo(base, path string) string {
	if r, err := filepath.Rel(base, path); err == nil {
		return r
	}
	return path
}
