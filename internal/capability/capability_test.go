package capability

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/stratt-sh/stratt/internal/detect"
)

// fakeEngine is a minimal in-memory Engine for runner / resolver tests.
// Tests construct one directly; production code never uses this.
type fakeEngine struct {
	name   string
	status EngineStatus
	runErr error
	calls  int
}

func (f *fakeEngine) Name() string         { return f.name }
func (f *fakeEngine) Status() EngineStatus { return f.status }
func (f *fakeEngine) Run(_ context.Context, _ []string) error {
	f.calls++
	return f.runErr
}

func TestResolverHasStack(t *testing.T) {
	r := &Resolver{
		report: detect.Report{
			Stacks: []detect.Stack{
				{Name: "go"},
				{Name: "docker"},
			},
		},
	}
	if !r.HasStack("go") {
		t.Error("expected HasStack(go) = true")
	}
	if !r.HasStack("docker") {
		t.Error("expected HasStack(docker) = true")
	}
	if r.HasStack("python+uv") {
		t.Error("expected HasStack(python+uv) = false")
	}
}

func TestResolverStacks(t *testing.T) {
	want := []detect.Stack{{Name: "go", Signal: "go.mod"}}
	r := &Resolver{report: detect.Report{Stacks: want}}
	if got := r.Stacks(); !reflect.DeepEqual(got, want) {
		t.Errorf("Stacks: got %v, want %v", got, want)
	}
}

// TestResolveUnknownCommand exercises the safety hatch in Resolve: any
// command not in the switch returns a Resolution with nil Engine rather
// than panicking.
func TestResolveUnknownCommand(t *testing.T) {
	r := New(t.TempDir())
	res := r.Resolve("totally-not-a-command")
	if res.Engine != nil {
		t.Errorf("unknown command should return nil engine, got %+v", res)
	}
	if res.Command != "totally-not-a-command" {
		t.Errorf("Command field: got %q", res.Command)
	}
}

// TestResolveAllReturnsAllUniversalCommands guards against accidental
// drift between UniversalCommands and ResolveAll.
func TestResolveAllReturnsAllUniversalCommands(t *testing.T) {
	r := New(t.TempDir())
	got := r.ResolveAll()
	if len(got) != len(UniversalCommands) {
		t.Fatalf("ResolveAll returned %d entries, UniversalCommands has %d",
			len(got), len(UniversalCommands))
	}
	for i, c := range UniversalCommands {
		if got[i].Command != c {
			t.Errorf("position %d: got %q, want %q", i, got[i].Command, c)
		}
	}
}

func TestEngineStatusConstants(t *testing.T) {
	// Sanity check: the three statuses are distinct.
	statuses := map[EngineStatus]bool{
		StatusReady:       true,
		StatusMissingTool: true,
		StatusPending:     true,
	}
	if len(statuses) != 3 {
		t.Fatalf("expected 3 distinct EngineStatus values, got %d", len(statuses))
	}
}

func TestFakeEngineRunIsTracked(t *testing.T) {
	// Sanity check that the test fixture itself works as expected,
	// so failures in other tests aren't caused by a broken fake.
	want := errors.New("boom")
	f := &fakeEngine{name: "test", runErr: want}
	if got := f.Run(context.Background(), nil); got != want {
		t.Errorf("got %v, want %v", got, want)
	}
	if f.calls != 1 {
		t.Errorf("expected 1 call, got %d", f.calls)
	}
}

// TestSubprojectFanOutAutoDetect — emulsia shape: no language stack at the
// root, so `test` fans out to backend (pytest) and frontend (npm test).
func TestSubprojectFanOutAutoDetect(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "Dockerfile")
	touch(t, dir, "backend/pyproject.toml")
	touch(t, dir, "backend/uv.lock")
	touch(t, dir, "frontend/package.json")
	touch(t, dir, "frontend/package-lock.json")

	r := New(dir)
	if len(r.Subprojects()) != 2 {
		t.Fatalf("expected 2 subprojects, got %v", r.Subprojects())
	}
	got := r.Resolve("test").Engine
	if got == nil {
		t.Fatal("expected a fan-out test engine, got nil")
	}
	want := "backend: uv run --all-extras --all-groups pytest; frontend: npm test"
	if got.Name() != want {
		t.Errorf("test fan-out:\n got  %q\n want %q", got.Name(), want)
	}
}

// TestSubprojectFanOutOnlyResolving — `build` only fans out to
// subprojects whose chain resolves build; a subproject with no build engine is
// silently dropped rather than producing an empty step.
func TestSubprojectFanOutOnlyResolving(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "Dockerfile")
	touch(t, dir, "backend/pyproject.toml")
	touch(t, dir, "backend/uv.lock")
	touch(t, dir, "frontend/package.json")
	touch(t, dir, "frontend/package-lock.json")

	// Both resolve build (uv build / npm run build); confirm both appear.
	got := New(dir).Resolve("build").Engine
	if got == nil {
		t.Fatal("expected build engine")
	}
	if want := "backend: uv build; frontend: npm run build"; got.Name() != want {
		t.Errorf("build fan-out:\n got  %q\n want %q", got.Name(), want)
	}
}

// TestSubprojectRepoGlobalVerbsStayAtRoot — deploy/release resolve against
// the root even in a workspace, so a monorepo's single kustomize deploy
// keeps working and does not fan out.
func TestSubprojectRepoGlobalVerbsStayAtRoot(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "deploy/overlays/prod/kustomization.yaml")
	touch(t, dir, "backend/pyproject.toml")
	touch(t, dir, "backend/uv.lock")

	r := New(dir)
	deploy := r.Resolve("deploy").Engine
	if deploy == nil {
		t.Fatal("expected root kustomize deploy engine")
	}
	if deploy.Name() != "kustomize image bump (deploy/overlays/<env>/kustomization.yaml)" {
		t.Errorf("deploy should be root kustomize delegate, got %q", deploy.Name())
	}
}

// TestPrimaryRootStackSuppressesAutoSubprojects — a repo with a language
// stack at the root does NOT auto-detect subprojects (backward compatible);
// `test` resolves to the root engine exactly as before.
func TestPrimaryRootStackSuppressesAutoSubprojects(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "go.mod")
	touch(t, dir, "frontend/package.json")
	touch(t, dir, "frontend/package-lock.json")

	r := New(dir)
	if len(r.Subprojects()) != 0 {
		t.Fatalf("primary root stack should suppress auto subprojects, got %v", r.Subprojects())
	}
	if got := r.Resolve("test").Engine; got == nil || got.Name() != "go test ./..." {
		t.Errorf("expected root `go test ./...`, got %v", got)
	}
}

// TestExplicitSubprojectsOptInOverRoot — declaring [[subprojects]] fans out even
// when the root has a stack, and honors the declared order and the `only`
// verb filter.
func TestExplicitSubprojectsOptInOverRoot(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "go.mod") // root has a primary stack
	touch(t, dir, "frontend/package.json")
	touch(t, dir, "frontend/package-lock.json")

	r := NewWithSubprojects(dir, []SubprojectSpec{
		{Dir: ".", Only: []string{"test"}},
		{Dir: "frontend"},
	})
	// test fans out to both root (.) and frontend.
	got := r.Resolve("test").Engine
	if got == nil {
		t.Fatal("expected fan-out test engine")
	}
	if want := ".: go test ./...; frontend: npm test"; got.Name() != want {
		t.Errorf("test fan-out:\n got  %q\n want %q", got.Name(), want)
	}
	// lint is filtered out of the root subproject (only=test), so only
	// frontend lints.
	lint := r.Resolve("lint").Engine
	if lint == nil {
		t.Fatal("expected lint engine for frontend")
	}
	if want := "frontend: npm run lint"; lint.Name() != want {
		t.Errorf("lint fan-out (root filtered by only):\n got  %q\n want %q", lint.Name(), want)
	}
}

// TestExplicitSubprojectsSkipsEmptyDir — a declared subproject pointing at a dir
// with no detectable stack contributes nothing (no panic, no empty step).
func TestExplicitSubprojectsSkipsEmptyDir(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "backend/pyproject.toml")
	touch(t, dir, "backend/uv.lock")

	r := NewWithSubprojects(dir, []SubprojectSpec{
		{Dir: "backend"},
		{Dir: "does-not-exist"},
	})
	if len(r.Subprojects()) != 1 {
		t.Fatalf("expected only the resolvable subproject, got %v", r.Subprojects())
	}
}

// TestSubprojectLintCheckFansOutNonMutating — `lint --check` fans out with
// each subproject in its read-only (no --fix) mode.
func TestSubprojectLintCheckFansOut(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "Dockerfile")
	touch(t, dir, "backend/pyproject.toml")
	touch(t, dir, "backend/uv.lock")

	got := New(dir).ResolveLintCheck()
	if got == nil {
		t.Fatal("expected lint-check engine")
	}
	if want := "backend: uv run --all-extras --all-groups ruff check"; got.Name() != want {
		t.Errorf("lint --check fan-out:\n got  %q\n want %q", got.Name(), want)
	}
}

// TestSubprojectSkipExcludesVerb — `skip` is a denylist: the named verbs
// don't fan out to that subproject, the rest do.  This is emulsia's shape
// (backend skips build; frontend skips test+format).
func TestSubprojectSkipExcludesVerb(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "backend/pyproject.toml")
	touch(t, dir, "backend/uv.lock")
	touch(t, dir, "frontend/package.json")
	touch(t, dir, "frontend/package-lock.json")

	r := NewWithSubprojects(dir, []SubprojectSpec{
		{Dir: "backend", Skip: []string{"build"}},
		{Dir: "frontend", Skip: []string{"test", "format"}},
	})

	// build: backend skips it → frontend only.
	if got := r.Resolve("build").Engine; got == nil || got.Name() != "frontend: npm run build" {
		t.Errorf("build: got %v", got)
	}
	// test: frontend skips it → backend only.
	if got := r.Resolve("test").Engine; got == nil ||
		got.Name() != "backend: uv run --all-extras --all-groups pytest" {
		t.Errorf("test: got %v", got)
	}
	// lint: neither skips → both.
	if got := r.Resolve("lint").Engine; got == nil ||
		got.Name() != "backend: uv run --all-extras --all-groups ruff check --fix; frontend: npm run lint" {
		t.Errorf("lint: got %v", got)
	}
}
