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
