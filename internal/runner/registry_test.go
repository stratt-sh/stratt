package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stratt-sh/stratt/internal/capability"
	"github.com/stratt-sh/stratt/internal/config"
)

func touch(t *testing.T, root, rel string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// goRepo returns a temp directory with a go.mod so the resolver finds
// a `go` stack and populates the standard built-in tasks.
func goRepo(t *testing.T) string {
	dir := t.TempDir()
	touch(t, dir, "go.mod")
	return dir
}

func TestBuildRegistryIncludesBuiltins(t *testing.T) {
	dir := goRepo(t)
	res := capability.New(dir)
	reg, err := BuildRegistry(res, &config.Project{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"build", "test", "lint", "lock"} {
		task := reg.Lookup(name)
		if task == nil {
			t.Errorf("expected built-in %q in registry", name)
			continue
		}
		if task.Source != SourceBuiltin {
			t.Errorf("%s: source got %v, want built-in", name, task.Source)
		}
		if task.Engine == nil {
			t.Errorf("%s: built-in should carry an Engine", name)
		}
	}
}

func TestBuildRegistryUserTaskAddMode(t *testing.T) {
	dir := goRepo(t)
	res := capability.New(dir)
	proj := &config.Project{
		Tasks: map[string]config.Task{
			"deploy-staging": {
				Description: "Deploy staging",
				Run:         []string{"kubectl apply -k deploy/overlays/staging"},
				Enabled:     true,
			},
		},
	}
	reg, err := BuildRegistry(res, proj)
	if err != nil {
		t.Fatal(err)
	}
	got := reg.Lookup("deploy-staging")
	if got == nil {
		t.Fatal("user task missing from registry")
	}
	if got.Source != SourceUser {
		t.Errorf("source: got %v, want user", got.Source)
	}
	if len(got.Run) != 1 || got.Run[0] != "kubectl apply -k deploy/overlays/staging" {
		t.Errorf("run: got %v", got.Run)
	}
	if got.Engine != nil {
		t.Errorf("user task should not have Engine, got %v", got.Engine)
	}
}

func TestBuildRegistryOverrideMode(t *testing.T) {
	dir := goRepo(t)
	res := capability.New(dir)
	proj := &config.Project{
		Tasks: map[string]config.Task{
			"test": {
				Run:     []string{"my-custom-test-runner"},
				Enabled: true,
			},
		},
	}
	reg, err := BuildRegistry(res, proj)
	if err != nil {
		t.Fatal(err)
	}
	got := reg.Lookup("test")
	if got.Source != SourceOverridden {
		t.Errorf("source: got %v, want overridden", got.Source)
	}
	if got.Engine != nil {
		t.Errorf("override should discard built-in engine, got %v", got.Engine)
	}
	if len(got.Run) != 1 || got.Run[0] != "my-custom-test-runner" {
		t.Errorf("run: got %v", got.Run)
	}
}

func TestBuildRegistryAugmentMode(t *testing.T) {
	dir := goRepo(t)
	res := capability.New(dir)
	proj := &config.Project{
		Tasks: map[string]config.Task{
			"test": {
				Before:  []string{"docker compose up -d"},
				After:   []string{"docker compose down"},
				Enabled: true,
			},
		},
	}
	reg, err := BuildRegistry(res, proj)
	if err != nil {
		t.Fatal(err)
	}
	got := reg.Lookup("test")
	if got.Source != SourceAugmented {
		t.Errorf("source: got %v, want augmented", got.Source)
	}
	if got.Engine == nil {
		t.Error("augment should preserve built-in engine")
	}
	if len(got.Before) != 1 || got.Before[0] != "docker compose up -d" {
		t.Errorf("before: got %v", got.Before)
	}
}

// TestBuildRegistryOverrideForbidsBeforeAfter — `run` is mutually
// exclusive with `before`/`after`.  This guards R2.6.1.
func TestBuildRegistryOverrideForbidsBeforeAfter(t *testing.T) {
	dir := goRepo(t)
	res := capability.New(dir)
	proj := &config.Project{
		Tasks: map[string]config.Task{
			"test": {
				Run:     []string{"x"},
				Before:  []string{"y"},
				Enabled: true,
			},
		},
	}
	if _, err := BuildRegistry(res, proj); err == nil {
		t.Fatal("expected error for run+before combination")
	}
}

func TestBuildRegistryDisable(t *testing.T) {
	dir := goRepo(t)
	res := capability.New(dir)
	// Built-in `test` is disabled by the user.
	proj := &config.Project{
		Tasks: map[string]config.Task{
			"test": {Enabled: false},
			"foo":  {Run: []string{"echo foo"}, Enabled: false},
		},
	}
	reg, err := BuildRegistry(res, proj)
	if err != nil {
		t.Fatal(err)
	}
	if reg.Lookup("test") != nil {
		t.Error("disabled built-in should be removed from registry")
	}
	if reg.Lookup("foo") != nil {
		t.Error("disabled user task should not be added")
	}
}

func TestBuildRegistryHelperShadowsBuiltinForbidden(t *testing.T) {
	dir := goRepo(t)
	res := capability.New(dir)
	proj := &config.Project{
		Helpers: map[string]config.Task{
			"test": {Run: []string{"x"}, Enabled: true},
		},
	}
	if _, err := BuildRegistry(res, proj); err == nil {
		t.Fatal("expected error for helper shadowing built-in")
	}
}

func TestBuildRegistryUnknownTaskReference(t *testing.T) {
	dir := goRepo(t)
	res := capability.New(dir)
	proj := &config.Project{
		Tasks: map[string]config.Task{
			"deploy": {
				Tasks:   []string{"missing"},
				Run:     []string{"x"},
				Enabled: true,
			},
		},
	}
	if _, err := BuildRegistry(res, proj); err == nil {
		t.Fatal("expected error for unknown task reference")
	}
}

func TestBuildRegistryCycleDetection(t *testing.T) {
	dir := goRepo(t)
	res := capability.New(dir)
	proj := &config.Project{
		Tasks: map[string]config.Task{
			"a": {Tasks: []string{"b"}, Run: []string{"x"}, Enabled: true},
			"b": {Tasks: []string{"c"}, Run: []string{"x"}, Enabled: true},
			"c": {Tasks: []string{"a"}, Run: []string{"x"}, Enabled: true},
		},
	}
	_, err := BuildRegistry(res, proj)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("error should name the cycle; got %v", err)
	}
}

func TestBuildRegistryEmptyTaskRejected(t *testing.T) {
	dir := goRepo(t)
	res := capability.New(dir)
	// User task with no run and no tasks: nothing to do.
	proj := &config.Project{
		Tasks: map[string]config.Task{
			"empty": {Enabled: true},
		},
	}
	if _, err := BuildRegistry(res, proj); err == nil {
		t.Fatal("expected error for empty user task")
	}
}

// TestBuildRegistrySynthesizesStyleComposite — `style` should be auto-
// constructed from format + lint when both engines resolve.
func TestBuildRegistrySynthesizesStyleComposite(t *testing.T) {
	dir := goRepo(t)
	res := capability.New(dir)
	reg, err := BuildRegistry(res, &config.Project{})
	if err != nil {
		t.Fatal(err)
	}
	style := reg.Lookup("style")
	if style == nil {
		t.Fatal("expected `style` to be synthesized")
	}
	if style.Source != SourceBuiltin {
		t.Errorf("source: got %v", style.Source)
	}
	if style.Engine != nil {
		t.Errorf("composite should not carry an Engine: got %v", style.Engine)
	}
	want := []string{"format", "lint"}
	if len(style.Tasks) != len(want) {
		t.Fatalf("tasks: got %v, want %v", style.Tasks, want)
	}
	for i := range want {
		if style.Tasks[i] != want[i] {
			t.Errorf("tasks[%d]: got %q, want %q", i, style.Tasks[i], want[i])
		}
	}
}

// TestBuildRegistrySynthesizesAllComposite — `all` should chain every
// detected verification stage.  For a Go repo: format, lint, test
// (no docs unless mkdocs/sphinx detected).
func TestBuildRegistrySynthesizesAllComposite(t *testing.T) {
	dir := goRepo(t)
	res := capability.New(dir)
	reg, err := BuildRegistry(res, &config.Project{})
	if err != nil {
		t.Fatal(err)
	}
	all := reg.Lookup("all")
	if all == nil {
		t.Fatal("expected `all` to be synthesized")
	}
	// `all` includes `sync` first so the env is current before tests.
	// For a Go repo this is `sync + format + lint + test`.
	want := []string{"sync", "format", "lint", "test"}
	if len(all.Tasks) != len(want) {
		t.Fatalf("tasks: got %v, want %v", all.Tasks, want)
	}
	for i := range want {
		if all.Tasks[i] != want[i] {
			t.Errorf("tasks[%d]: got %q, want %q", i, all.Tasks[i], want[i])
		}
	}
}

// TestBuildRegistryAllIncludesDocsWhenPresent — adding mkdocs.yml extends
// `all` to include docs.
func TestBuildRegistryAllIncludesDocsWhenPresent(t *testing.T) {
	dir := goRepo(t)
	touch(t, dir, "mkdocs.yml")
	res := capability.New(dir)
	reg, err := BuildRegistry(res, &config.Project{})
	if err != nil {
		t.Fatal(err)
	}
	all := reg.Lookup("all")
	if all == nil {
		t.Fatal("expected `all` to be synthesized")
	}
	want := []string{"sync", "format", "lint", "test", "docs"}
	if len(all.Tasks) != len(want) {
		t.Fatalf("tasks: got %v, want %v", all.Tasks, want)
	}
}

// TestBuildRegistryAllShrinksWhenConstituentDisabled — disabling test
// silently removes it from `all`'s Tasks list (R2.6.10 plus the
// "let users override if all should be less than all" policy).
func TestBuildRegistryAllShrinksWhenConstituentDisabled(t *testing.T) {
	dir := goRepo(t)
	res := capability.New(dir)
	proj := &config.Project{
		Tasks: map[string]config.Task{
			"test": {Enabled: false},
		},
	}
	reg, err := BuildRegistry(res, proj)
	if err != nil {
		t.Fatal(err)
	}
	all := reg.Lookup("all")
	if all == nil {
		t.Fatal("expected `all` to remain")
	}
	for _, e := range all.Tasks {
		if e == "test" {
			t.Errorf("disabled `test` should have been pruned from `all`; got %v", all.Tasks)
		}
	}
}

// TestBuildRegistryAllOverrideReplacesEntirely — user can override `all`
// to be a narrower set.
func TestBuildRegistryAllOverrideReplacesEntirely(t *testing.T) {
	dir := goRepo(t)
	res := capability.New(dir)
	proj := &config.Project{
		Tasks: map[string]config.Task{
			"all": {
				Tasks:   []string{"format"},
				Enabled: true,
				// User defines `all` as just format — no Run, no body.
				// This is augment-shape on a composite; the existing
				// composite's Tasks should be PRESERVED behind the user's
				// additions per R2.6.5.
			},
		},
	}
	reg, err := BuildRegistry(res, proj)
	if err != nil {
		t.Fatal(err)
	}
	all := reg.Lookup("all")
	if all == nil {
		t.Fatal("expected `all`")
	}
	// User's `format` comes first, then the existing composite's
	// [sync, format, lint, test] for a Go repo.  Duplicates are fine
	// — RunTask runs each in order.
	want := []string{"format", "sync", "format", "lint", "test"}
	if len(all.Tasks) != len(want) {
		t.Errorf("augmented `all`: got %v, want %v", all.Tasks, want)
	}
}

func TestRegistryTasksIsSorted(t *testing.T) {
	dir := goRepo(t)
	res := capability.New(dir)
	proj := &config.Project{
		Tasks: map[string]config.Task{
			"zebra":  {Run: []string{"x"}, Enabled: true},
			"alpha":  {Run: []string{"x"}, Enabled: true},
			"middle": {Run: []string{"x"}, Enabled: true},
		},
	}
	reg, _ := BuildRegistry(res, proj)
	tasks := reg.Tasks()
	for i := 1; i < len(tasks); i++ {
		if tasks[i-1].Name > tasks[i].Name {
			t.Errorf("Tasks() not sorted: %s before %s", tasks[i-1].Name, tasks[i].Name)
		}
	}
}

// TestRunTaskExecutesEngine — task with built-in body invokes the
// engine when run.
func TestRunTaskExecutesEngine(t *testing.T) {
	eng := &fakeEngine{name: "fake"}
	reg := &Registry{tasks: map[string]*Task{
		"x": {Name: "x", Source: SourceBuiltin, Engine: eng},
	}}
	r := New(Options{Registry: reg, Stderr: discard{}, Stdout: discard{}})
	if err := r.RunTask(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if eng.calls != 1 {
		t.Errorf("engine should be invoked once, got %d", eng.calls)
	}
}

// TestRunTaskRecursesIntoTasksField — task with `tasks = [...]` runs
// each in declared order.  Verified via shell-side trace file because
// fakeEngine is intentionally minimal (no per-call hooks).
func TestRunTaskRecursesIntoTasksField(t *testing.T) {
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace.txt")
	reg := &Registry{tasks: map[string]*Task{
		"a":        {Name: "a", Source: SourceUser, Run: []string{"echo a >> " + trace}},
		"b":        {Name: "b", Source: SourceUser, Run: []string{"echo b >> " + trace}},
		"pipeline": {Name: "pipeline", Source: SourceUser, Tasks: []string{"a", "b"}},
	}}
	r := New(Options{Registry: reg, Stderr: discard{}, Stdout: discard{}, CWD: dir})
	if err := r.RunTask(context.Background(), "pipeline"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if want := "a\nb\n"; string(body) != want {
		t.Errorf("order: got %q, want %q", body, want)
	}
}

func TestRunTaskUnknownTaskReturnsErr(t *testing.T) {
	reg := &Registry{tasks: map[string]*Task{}}
	r := New(Options{Registry: reg, Stderr: discard{}, Stdout: discard{}})
	err := r.RunTask(context.Background(), "nope")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunTaskNilRegistry(t *testing.T) {
	r := New(Options{Stderr: discard{}, Stdout: discard{}})
	err := r.RunTask(context.Background(), "anything")
	if err == nil {
		t.Fatal("expected error when no registry configured")
	}
}

// TestRunTaskShellCommand — pure user task with `run` field runs shell
// commands and captures their failures.
func TestRunTaskShellCommandSuccess(t *testing.T) {
	dir := t.TempDir()
	reg := &Registry{tasks: map[string]*Task{
		"echo": {
			Name:   "echo",
			Source: SourceUser,
			Run:    []string{"true"},
		},
	}}
	r := New(Options{Registry: reg, Stderr: discard{}, Stdout: discard{}, CWD: dir})
	if err := r.RunTask(context.Background(), "echo"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunTaskShellCommandFailurePropagates(t *testing.T) {
	dir := t.TempDir()
	reg := &Registry{tasks: map[string]*Task{
		"fail": {
			Name:   "fail",
			Source: SourceUser,
			Run:    []string{"false"},
		},
	}}
	r := New(Options{Registry: reg, Stderr: discard{}, Stdout: discard{}, CWD: dir})
	err := r.RunTask(context.Background(), "fail")
	if err == nil {
		t.Fatal("expected error from `false` exit 1")
	}
}

// TestRunTaskExecutionOrder — before → tasks → run → after, per R2.6.5.
// We log each step to a buffer and verify the sequence.
func TestRunTaskExecutionOrder(t *testing.T) {
	dir := t.TempDir()
	// All steps write to a file we can read in order afterward.
	out := filepath.Join(dir, "trace.txt")
	reg := &Registry{tasks: map[string]*Task{
		"sub": {
			Name:   "sub",
			Source: SourceUser,
			Run:    []string{"echo sub >> " + out},
		},
		"main": {
			Name:   "main",
			Source: SourceUser,
			Before: []string{"echo before >> " + out},
			Tasks:  []string{"sub"},
			Run:    []string{"echo body >> " + out},
			After:  []string{"echo after >> " + out},
		},
	}}
	r := New(Options{Registry: reg, Stderr: discard{}, Stdout: discard{}, CWD: dir})
	if err := r.RunTask(context.Background(), "main"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want := "before\nsub\nbody\nafter\n"
	if string(got) != want {
		t.Errorf("execution order:\n  got:  %q\n  want: %q", string(got), want)
	}
}

// discard is a no-op io.Writer for silencing runner announcements in tests.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
