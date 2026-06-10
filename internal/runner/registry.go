package runner

import (
	"fmt"
	"sort"

	"github.com/stratt-sh/stratt/internal/capability"
	"github.com/stratt-sh/stratt/internal/config"
)

// Source describes where a task came from.  Used by `stratt doctor` and
// `stratt help` to disambiguate user customizations from defaults.
type Source string

const (
	SourceBuiltin    Source = "built-in"
	SourceUser       Source = "user"
	SourceOverridden Source = "overridden"
	SourceAugmented  Source = "augmented"
	SourceDisabled   Source = "disabled"
)

// Task is the runtime-merged form of a stratt task.  Built-in (engine-
// backed) and user (shell-backed) tasks share this single shape.
//
// Field semantics by mode (per R2.6.1):
//
//   - Built-in only:        Engine != nil, UserRun nil, Before/After empty.
//   - Override built-in:    Engine == nil, UserRun != nil (built-in body discarded).
//   - Augment built-in:     Engine != nil, UserRun nil, Before/After populated.
//   - Pure user task:       Engine == nil, UserRun != nil.
//   - Disabled:             Enabled == false; not entered into the registry.
type Task struct {
	Name        string
	Description string
	Hidden      bool   // came from [helpers.X]
	Source      Source // resolved source for `stratt doctor`/`stratt help`
	Tasks       []string
	Before      []string
	Run         []string // shell commands; nil if Engine is the body
	After       []string
	Engine      capability.Engine // nil if Run is set (override) or no built-in
}

// Registry is the unified task namespace (built-ins + user-defined).
// Construction validates the task graph; lookups are then cheap and
// guaranteed sound.
type Registry struct {
	tasks map[string]*Task
}

// BuildRegistry merges built-in tasks (from the resolver) with user-
// defined tasks (from the project config).  Returns an error for any
// invalid task graph: dangling deps, cycles, mode violations, or
// references to disabled tasks (R2.6.2 / R2.6.6).
func BuildRegistry(res *capability.Resolver, proj *config.Project) (*Registry, error) {
	r := &Registry{tasks: map[string]*Task{}}

	// Built-in tasks come first.  Every resolved engine becomes a Task
	// in the registry; the universal subcommand dispatch and `stratt run`
	// then share the same executor.
	//
	// Composite engines (style, all) need special handling: the runtime
	// Task records the composition in its Tasks field, not as an Engine.
	for _, resolution := range res.ResolveAll() {
		if resolution.Engine == nil {
			continue
		}
		task := &Task{
			Name:        resolution.Command,
			Description: defaultBuiltinDescription(resolution.Command),
			Source:      SourceBuiltin,
		}
		if comp, ok := resolution.Engine.(capability.CompositeEngine); ok {
			task.Tasks = comp.CompositeMembers()
		} else {
			task.Engine = resolution.Engine
		}
		r.tasks[resolution.Command] = task
	}

	// Merge user-defined tasks and helpers.  Helpers carry Hidden=true;
	// otherwise the merge logic is identical.
	if proj != nil {
		for name, ut := range proj.Tasks {
			if err := r.merge(name, ut, false); err != nil {
				return nil, err
			}
		}
		for name, ut := range proj.Helpers {
			if existing, ok := r.tasks[name]; ok && existing.Source == SourceBuiltin {
				return nil, fmt.Errorf(
					"helper %q shadows a built-in command; built-ins must be in [tasks], not [helpers]",
					name)
			}
			if err := r.merge(name, ut, true); err != nil {
				return nil, err
			}
		}
	}

	// Validate the graph: all references must resolve and the graph must be acyclic.
	if err := r.validate(); err != nil {
		return nil, err
	}

	return r, nil
}

// merge applies one user-defined task to the registry, choosing
// override / augment / add semantics based on field shape.
func (r *Registry) merge(name string, ut config.Task, hidden bool) error {
	if !ut.Enabled {
		// `enabled = false` removes a task (R2.6.2).  Built-ins are
		// removed; pure user tasks are simply not added.
		//
		// Built-in composites (e.g. `all = [format, lint, test, docs]`)
		// silently shrink when a constituent is disabled — `stratt all`
		// just skips the disabled stage.  User-defined task references
		// to disabled tasks are still strict validation errors.
		//
		// Prune from both pure built-in composites and *augmented* ones:
		// because user tasks merge in random map order, the composite may
		// already have been turned into SourceAugmented (carrying the
		// built-in's members) by the time the disable is processed.  An
		// overridden composite (SourceOverridden) keeps its user-declared
		// members untouched — referencing a disabled task there is a
		// deliberate error, surfaced by validate().
		delete(r.tasks, name)
		for _, t := range r.tasks {
			if t.Source == SourceBuiltin || t.Source == SourceAugmented {
				t.Tasks = withoutString(t.Tasks, name)
			}
		}
		return nil
	}

	existing, isBuiltin := r.tasks[name]

	switch {
	case isBuiltin && len(ut.Run) > 0:
		// Override mode (R2.6.1).  Built-in body discarded.
		if len(ut.Before) > 0 || len(ut.After) > 0 {
			return fmt.Errorf(
				"task %q sets both `run` and `before`/`after`; "+
					"`before`/`after` are only valid in augment mode (no `run` field)", name)
		}
		t := &Task{
			Name:        name,
			Description: pick(ut.Description, existing.Description),
			Hidden:      hidden,
			Source:      SourceOverridden,
			Tasks:       append([]string(nil), ut.Tasks...),
			Run:         append([]string(nil), ut.Run...),
		}
		r.tasks[name] = t

	case isBuiltin && len(ut.Run) == 0:
		// Augment mode (R2.6.1).  Built-in body preserved, hooks attached.
		//
		// For composite built-ins (Engine nil but Tasks populated), the
		// composition's members must also be preserved — the user's
		// tasks list runs *before* the built-in composition.
		mergedTasks := append([]string(nil), ut.Tasks...)
		mergedTasks = append(mergedTasks, existing.Tasks...)
		t := &Task{
			Name:        name,
			Description: pick(ut.Description, existing.Description),
			Hidden:      hidden,
			Source:      SourceAugmented,
			Tasks:       mergedTasks,
			Before:      append([]string(nil), ut.Before...),
			Engine:      existing.Engine,
			After:       append([]string(nil), ut.After...),
		}
		r.tasks[name] = t

	case !isBuiltin && len(ut.Run) == 0 && len(ut.Tasks) == 0:
		// Pure user task with no run and no tasks does nothing
		// meaningful.  This is a likely user mistake; surface it.
		return fmt.Errorf("task %q has no `run` and no `tasks`; nothing to do", name)

	default:
		// Pure user task (add mode).  May or may not have a body — a
		// task with only `tasks = [...]` is a valid composition.
		t := &Task{
			Name:        name,
			Description: ut.Description,
			Hidden:      hidden,
			Source:      SourceUser,
			Tasks:       append([]string(nil), ut.Tasks...),
			Before:      append([]string(nil), ut.Before...),
			Run:         append([]string(nil), ut.Run...),
			After:       append([]string(nil), ut.After...),
		}
		r.tasks[name] = t
	}
	return nil
}

// validate ensures all task references resolve and the graph is acyclic.
func (r *Registry) validate() error {
	for name, t := range r.tasks {
		for _, dep := range t.Tasks {
			if _, ok := r.tasks[dep]; !ok {
				return fmt.Errorf("task %q references unknown task %q", name, dep)
			}
		}
	}

	// Cycle detection via depth-first search with three colors.
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var stack []string

	var visit func(name string) error
	visit = func(name string) error {
		color[name] = gray
		stack = append(stack, name)
		defer func() {
			stack = stack[:len(stack)-1]
			color[name] = black
		}()
		t := r.tasks[name]
		for _, dep := range t.Tasks {
			switch color[dep] {
			case gray:
				return fmt.Errorf("circular task reference: %s → %s",
					joinPath(stack), dep)
			case white:
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		return nil
	}

	// Visit in a deterministic order so error messages are reproducible.
	names := make([]string, 0, len(r.tasks))
	for name := range r.tasks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if color[name] == 0 {
			if err := visit(name); err != nil {
				return err
			}
		}
	}
	return nil
}

// Lookup returns the task with the given name.  Returns nil if no such
// task exists.
func (r *Registry) Lookup(name string) *Task {
	return r.tasks[name]
}

// Tasks returns all registered task names in sorted order.  Includes
// hidden helpers.  Filter using Task.Hidden if you only want the public
// surface (e.g. for `stratt help`).
func (r *Registry) Tasks() []*Task {
	out := make([]*Task, 0, len(r.tasks))
	for _, t := range r.tasks {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// defaultBuiltinDescription supplies a short description for built-in
// tasks that don't have one assigned upstream.  Keeps `stratt help` from
// being a wall of blank cells.
func defaultBuiltinDescription(name string) string {
	switch name {
	case "build":
		return "Build the project using the detected toolchain"
	case "test":
		return "Run tests using the detected test runner"
	case "lint":
		return "Run linters using the detected linter"
	case "format":
		return "Run formatters using the detected formatter"
	case "setup":
		return "Perform first-time project setup"
	case "sync":
		return "Sync dependencies from the lockfile"
	case "lock":
		return "Update the lockfile from the manifest"
	case "upgrade":
		return "Upgrade all dependencies"
	case "clean":
		return "Remove build / cache artifacts"
	case "release":
		return "Bump version, commit, tag, and push"
	case "deploy":
		return "Bump Kustomize image tags"
	case "docs":
		return "Build or serve documentation"
	case "style":
		return "Run formatters and linters together"
	case "all":
		return "Run the full verification suite (everything detected)"
	}
	return ""
}

func pick(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func joinPath(stack []string) string {
	s := ""
	for i, e := range stack {
		if i > 0 {
			s += " → "
		}
		s += e
	}
	return s
}

// withoutString returns slice with every occurrence of needle removed,
// preserving the order of the remaining elements.  Used when a disabled
// task is removed from a built-in composite's Tasks list.
func withoutString(slice []string, needle string) []string {
	out := slice[:0]
	for _, s := range slice {
		if s != needle {
			out = append(out, s)
		}
	}
	return out
}
