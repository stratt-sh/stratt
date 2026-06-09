package capability

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// execEngine is the standard Engine implementation: a single external
// command run in the repo root.  Most engines are just this with
// different argv.
type execEngine struct {
	tool    string   // the binary on PATH, e.g. "uv" or "go"
	argv    []string // arguments after the tool name
	display string   // optional override for Name(); defaults to "tool argv..."
	cwd     string   // directory to run in; defaults to repo root
}

func (e *execEngine) Name() string {
	if e.display != "" {
		return e.display
	}
	return strings.Join(append([]string{e.tool}, e.argv...), " ")
}

func (e *execEngine) Status() EngineStatus {
	if !available(e.tool) {
		return StatusMissingTool
	}
	return StatusReady
}

// Tool implements capability.Tooler — used by `stratt doctor` to
// surface install hints for missing binaries.
func (e *execEngine) Tool() string { return e.tool }

func (e *execEngine) Run(ctx context.Context, _ []string) error {
	cmd := exec.CommandContext(ctx, e.tool, e.argv...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if e.cwd != "" {
		cmd.Dir = e.cwd
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", e.Name(), err)
	}
	return nil
}

// shellEngine wraps a `sh -c <line>` invocation — used when a resolved
// backend needs shell composition (pipelines, `&&`, redirects).  Status
// reports Ready unconditionally; the shell handles missing-tool errors
// at run time with its own messaging.
type shellEngine struct {
	line    string
	display string
	dir     string // directory to run in; defaults to the process cwd
}

func (e *shellEngine) Name() string {
	if e.display != "" {
		return e.display
	}
	return e.line
}

func (e *shellEngine) Status() EngineStatus { return StatusReady }

func (e *shellEngine) Run(ctx context.Context, _ []string) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", e.line)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if e.dir != "" {
		cmd.Dir = e.dir
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", e.Name(), err)
	}
	return nil
}

// notImplementedEngine is a placeholder for engines that the requirements
// call for but aren't built yet.  It returns a clear error if invoked
// but reports a useful display name for `stratt doctor`.
type notImplementedEngine struct {
	display string
}

func (e *notImplementedEngine) Name() string         { return e.display }
func (e *notImplementedEngine) Status() EngineStatus { return StatusPending }
func (e *notImplementedEngine) Run(_ context.Context, _ []string) error {
	return fmt.Errorf("engine %q is not yet implemented", e.display)
}

// delegateEngine is a "resolver result that isn't executed by the
// runner" — used when the resolver wants to say "this is the backend
// for command X" but the actual execution path is a custom-shape
// subcommand (e.g. `stratt release`, `stratt deploy`).  Status reports
// Ready so `stratt doctor` shows no caveat.  Calling Run is a
// programming error and returns an instructive message.
type delegateEngine struct {
	display     string
	delegateCmd string // user-facing command to invoke instead
}

func (e *delegateEngine) Name() string         { return e.display }
func (e *delegateEngine) Status() EngineStatus { return StatusReady }
func (e *delegateEngine) Run(_ context.Context, _ []string) error {
	return fmt.Errorf("engine %q is dispatched by the %q subcommand, not the runner",
		e.display, e.delegateCmd)
}

// compositeEngine represents a built-in command that's a composition of
// other built-in commands (e.g. `stratt style` = `format + lint`).
//
// The Resolver returns one of these so `stratt doctor` can show the
// composition.  Actual execution is delegated to the task Registry,
// which expands the composition into a Task with a non-empty Tasks
// field.  Calling Run() directly is therefore a programming error.
//
// Members is the ordered list of constituent built-in task names.
type compositeEngine struct {
	display string
	members []string
}

// CompositeMembers exposes the constituent task names so the Registry
// builder can synthesize the runtime Task without re-deriving them.
func (e *compositeEngine) CompositeMembers() []string {
	out := make([]string, len(e.members))
	copy(out, e.members)
	return out
}

func (e *compositeEngine) Name() string         { return e.display }
func (e *compositeEngine) Status() EngineStatus { return StatusReady }
func (e *compositeEngine) Run(_ context.Context, _ []string) error {
	return fmt.Errorf("composite %q runs via the task registry, not the engine path", e.display)
}

// CompositeEngine is the publicly-typed assertion target the runner
// uses to recognize composites without depending on unexported types.
// Implementations must mirror compositeEngine's CompositeMembers method.
type CompositeEngine interface {
	Engine
	CompositeMembers() []string
}

// multiEngine runs a sequence of sub-engines under a single universal
// command.  Unlike compositeEngine (which routes back through the task
// registry by command name), multiEngine executes its inner engines
// directly — used when a single universal command needs to fan out to
// multiple tools that don't have their own universal-command names
// (e.g. lint = language-lint + actionlint).
type multiEngine struct {
	engines []Engine
}

func (e *multiEngine) Name() string {
	parts := make([]string, 0, len(e.engines))
	for _, sub := range e.engines {
		parts = append(parts, sub.Name())
	}
	return strings.Join(parts, " + ")
}

func (e *multiEngine) Status() EngineStatus {
	for _, sub := range e.engines {
		if s := sub.Status(); s != StatusReady {
			return s
		}
	}
	return StatusReady
}

func (e *multiEngine) Run(ctx context.Context, args []string) error {
	for _, sub := range e.engines {
		if err := sub.Run(ctx, args); err != nil {
			return err
		}
	}
	return nil
}

// Tools surfaces every underlying tool name so `stratt doctor` can
// list each missing dependency.  Implements MultiTooler.
func (e *multiEngine) Tools() []string {
	out := make([]string, 0, len(e.engines))
	for _, sub := range e.engines {
		if t, ok := sub.(Tooler); ok {
			if name := t.Tool(); name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}

// subprojectStep pairs a subproject relative directory with the
// engine resolved for it.  Dir is a display label ("backend", or "." for
// the repo root); the engine itself already carries the absolute working
// directory it runs in (set via setEngineDir).
type subprojectStep struct {
	dir    string
	engine Engine
}

// fanOutEngine fans a single universal command out across the stacks
// detected in a monorepo's subproject subdirectories — e.g. `stratt test`
// running pytest in backend/ and `npm test` in frontend/.  Subprojects run in
// declared order and execution stops on the first failure, matching
// stratt's serial, fail-fast model (R2.6.5).
//
// It is only constructed when a monorepo actually has subprojects; a
// single-stack repo resolves to its bare engine exactly as before, so
// existing behavior is unchanged.
type fanOutEngine struct {
	command     string
	subprojects []subprojectStep
}

func (e *fanOutEngine) Name() string {
	parts := make([]string, len(e.subprojects))
	for i, m := range e.subprojects {
		parts[i] = fmt.Sprintf("%s: %s", m.dir, m.engine.Name())
	}
	return strings.Join(parts, "; ")
}

// Status aggregates the subprojects: any non-ready subproject makes the whole
// fan-out non-ready, surfacing MissingTool ahead of Pending so doctor's
// install hints take precedence.
func (e *fanOutEngine) Status() EngineStatus {
	worst := StatusReady
	for _, m := range e.subprojects {
		switch m.engine.Status() {
		case StatusMissingTool:
			return StatusMissingTool
		case StatusPending:
			worst = StatusPending
		}
	}
	return worst
}

func (e *fanOutEngine) Run(ctx context.Context, args []string) error {
	for _, m := range e.subprojects {
		if err := m.engine.Run(ctx, args); err != nil {
			return fmt.Errorf("%s in %s: %w", e.command, m.dir, err)
		}
	}
	return nil
}

// Tools aggregates every subproject's missing-tool surface so doctor lists
// each dependency once.  Implements MultiTooler.
func (e *fanOutEngine) Tools() []string {
	var out []string
	for _, m := range e.subprojects {
		switch t := m.engine.(type) {
		case MultiTooler:
			out = append(out, t.Tools()...)
		case Tooler:
			if name := t.Tool(); name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}

// setEngineDir configures e to run in dir, recursing into multiEngine so
// every composed sub-engine inherits the directory.  Engines that don't
// execute a process (delegate / composite / not-implemented) are returned
// unchanged.  The engines passed here are freshly constructed by the
// resolver chains and never shared, so mutating them in place is safe.
func setEngineDir(e Engine, dir string) {
	switch t := e.(type) {
	case *execEngine:
		t.cwd = dir
	case *shellEngine:
		t.dir = dir
	case *multiEngine:
		for _, sub := range t.engines {
			setEngineDir(sub, dir)
		}
	}
}
