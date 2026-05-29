// Package runner executes resolved engines and task graphs.
//
// The runner has two entry points:
//   - RunEngine / RunResolution: execute a single resolved engine,
//     used by universal subcommands when no user customization is in play.
//   - RunTask: look up a task by name in a Registry and execute its full
//     before → tasks → run-or-engine → after sequence, recursing into
//     referenced tasks.  Used by `stratt run` and by any universal
//     subcommand that resolved via a Registry-aware path.
//
// Per R2.6.5 everything is serial in declared order; there is no
// parallel execution model.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/stratt-sh/stratt/internal/capability"
	"github.com/stratt-sh/stratt/internal/ui"
)

// ErrNoEngine is returned from Run when the command has no resolved
// engine in the current repo.  Callers should render a clear error
// message that includes the command name and detected stacks.
var ErrNoEngine = errors.New("no engine matched for this repo")

// Options configures a Runner.  Zero-value Options is fine; Run defaults
// to os.Stdout / os.Stderr.
type Options struct {
	// Stdout receives engine output.  Defaults to os.Stdout.  The runner
	// itself does not write to Stdout — engines do.
	Stdout io.Writer

	// Stderr receives the runner's own status messages (e.g. "→ uv run
	// pytest") and the engines' stderr.  Defaults to os.Stderr.
	Stderr io.Writer

	// CWD is the working directory for shell commands run by RunTask.
	// Defaults to the process's current working directory.
	CWD string

	// Registry is the task registry used by RunTask.  Optional for the
	// engine-only execution paths.
	Registry *Registry

	// CI suppresses interactive prompts; plumbed through to engines.
	CI bool

	// Quiet suppresses the "→ <engine>" status line; useful for piping output.
	Quiet bool

	// Style colorizes the runner's own status lines (→ engine, ▶ task,
	// + shell).  Optional; when nil, New installs a no-color style so
	// status lines render as plain text.
	Style *ui.Style
}

// Runner executes resolved engines.
type Runner struct {
	opts Options
}

// New returns a Runner with defaults filled in.
func New(opts Options) *Runner {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.CWD == "" {
		cwd, err := os.Getwd()
		if err == nil {
			opts.CWD = cwd
		}
	}
	if opts.Style == nil {
		// No style supplied: render status lines as plain text.
		opts.Style = ui.NewStyle(opts.Stdout, opts.Stderr, ui.ColorNever, ui.Normal)
	}
	return &Runner{opts: opts}
}

// RunResolution executes a Resolution (the result of capability.Resolver.Resolve).
// Returns ErrNoEngine if Resolution.Engine is nil so callers can render a
// helpful repo-specific error message.
//
// args is forwarded to the engine.  Most engines ignore it; positional-arg
// commands (e.g., `stratt deploy <env> <ver>`) consume it.
func (r *Runner) RunResolution(ctx context.Context, res capability.Resolution, args []string) error {
	if res.Engine == nil {
		return fmt.Errorf("%w: %s", ErrNoEngine, res.Command)
	}
	return r.RunEngine(ctx, res.Engine, args)
}

// RunEngine executes a single Engine.  Announces the engine to Stderr
// (unless Quiet) then hands off.
func (r *Runner) RunEngine(ctx context.Context, engine capability.Engine, args []string) error {
	if engine == nil {
		return ErrNoEngine
	}
	if !r.opts.Quiet {
		fmt.Fprint(r.opts.Stderr, r.opts.Style.Progress(engine.Name()))
	}
	return engine.Run(ctx, args)
}

// ErrUnknownTask is returned by RunTask when name doesn't appear in the
// configured Registry.
var ErrUnknownTask = errors.New("unknown task")

// RunTask executes the named task, recursing into its `tasks` field and
// running its before/run/after segments per R2.6.5.  Requires that the
// Runner was constructed with a non-nil Registry.
//
// Recursive invocations share the same context and runner; concurrent
// invocations of RunTask are not supported (and per R2.6.5 unnecessary
// since execution is serial).
func (r *Runner) RunTask(ctx context.Context, name string) error {
	if r.opts.Registry == nil {
		return errors.New("RunTask called without a Registry; configure Options.Registry")
	}
	task := r.opts.Registry.Lookup(name)
	if task == nil {
		return fmt.Errorf("%w: %s", ErrUnknownTask, name)
	}
	return r.runTask(ctx, task)
}

// runTask is the internal recursion target.  Order is per R2.6:
//
//	before → tasks → (run | engine body) → after
//
// Stops on first error.
func (r *Runner) runTask(ctx context.Context, task *Task) error {
	if !r.opts.Quiet && (len(task.Tasks) > 0 || len(task.Before) > 0 || len(task.After) > 0 || len(task.Run) > 0) {
		// Announce composite/user tasks so the output trail makes sense.
		// Pure-engine tasks announce themselves in RunEngine.
		if task.Engine == nil || len(task.Tasks) > 0 || len(task.Before) > 0 || len(task.After) > 0 {
			fmt.Fprint(r.opts.Stderr, r.opts.Style.Task(task.Name))
		}
	}

	for _, cmd := range task.Before {
		if err := r.execShell(ctx, cmd); err != nil {
			return fmt.Errorf("task %q: before: %w", task.Name, err)
		}
	}
	for _, sub := range task.Tasks {
		if err := r.RunTask(ctx, sub); err != nil {
			return err
		}
	}
	switch {
	case task.Engine != nil:
		if err := r.RunEngine(ctx, task.Engine, nil); err != nil {
			return fmt.Errorf("task %q: %w", task.Name, err)
		}
	case len(task.Run) > 0:
		for _, cmd := range task.Run {
			if err := r.execShell(ctx, cmd); err != nil {
				return fmt.Errorf("task %q: run: %w", task.Name, err)
			}
		}
	}
	for _, cmd := range task.After {
		if err := r.execShell(ctx, cmd); err != nil {
			return fmt.Errorf("task %q: after: %w", task.Name, err)
		}
	}
	return nil
}

// execShell runs a single shell command in the configured CWD, streaming
// stdout/stderr.  Uses `sh -c` to support shell features (pipes, &&, etc.)
// per Make-style task semantics.
func (r *Runner) execShell(ctx context.Context, cmdStr string) error {
	if !r.opts.Quiet {
		fmt.Fprint(r.opts.Stderr, r.opts.Style.ShellCmd(cmdStr))
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	cmd.Stdin = os.Stdin
	cmd.Stdout = r.opts.Stdout
	cmd.Stderr = r.opts.Stderr
	cmd.Dir = r.opts.CWD
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", cmdStr, err)
	}
	return nil
}
