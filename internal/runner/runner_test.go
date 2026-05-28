package runner

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stratt-sh/stratt/internal/capability"
)

// fakeEngine is a controllable Engine for testing the runner.
type fakeEngine struct {
	name   string
	status capability.EngineStatus
	runErr error
	calls  int
	args   []string
}

func (f *fakeEngine) Name() string                    { return f.name }
func (f *fakeEngine) Status() capability.EngineStatus { return f.status }
func (f *fakeEngine) Run(_ context.Context, args []string) error {
	f.calls++
	f.args = args
	return f.runErr
}

func TestRunEngineAnnouncesByDefault(t *testing.T) {
	var stderr bytes.Buffer
	r := New(Options{Stderr: &stderr})
	eng := &fakeEngine{name: "uv run pytest"}
	if err := r.RunEngine(context.Background(), eng, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "→ uv run pytest") {
		t.Errorf("expected announce line; got %q", stderr.String())
	}
	if eng.calls != 1 {
		t.Errorf("engine should run once, got %d", eng.calls)
	}
}

func TestRunEngineQuietSuppressesAnnounce(t *testing.T) {
	var stderr bytes.Buffer
	r := New(Options{Stderr: &stderr, Quiet: true})
	eng := &fakeEngine{name: "uv run pytest"}
	if err := r.RunEngine(context.Background(), eng, nil); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Errorf("expected silent stderr in quiet mode; got %q", stderr.String())
	}
}

func TestRunEnginePropagatesError(t *testing.T) {
	want := errors.New("engine failed")
	r := New(Options{Stderr: &bytes.Buffer{}})
	eng := &fakeEngine{name: "x", runErr: want}
	got := r.RunEngine(context.Background(), eng, nil)
	if !errors.Is(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRunEngineForwardsArgs(t *testing.T) {
	r := New(Options{Stderr: &bytes.Buffer{}})
	eng := &fakeEngine{name: "x"}
	want := []string{"prod", "1.14.1"}
	if err := r.RunEngine(context.Background(), eng, want); err != nil {
		t.Fatal(err)
	}
	if len(eng.args) != len(want) {
		t.Fatalf("args: got %v, want %v", eng.args, want)
	}
	for i := range want {
		if eng.args[i] != want[i] {
			t.Errorf("args[%d]: got %q, want %q", i, eng.args[i], want[i])
		}
	}
}

func TestRunEngineNilReturnsErrNoEngine(t *testing.T) {
	r := New(Options{Stderr: &bytes.Buffer{}})
	err := r.RunEngine(context.Background(), nil, nil)
	if !errors.Is(err, ErrNoEngine) {
		t.Errorf("got %v, want ErrNoEngine", err)
	}
}

func TestRunResolutionWrapsCommandName(t *testing.T) {
	r := New(Options{Stderr: &bytes.Buffer{}})
	res := capability.Resolution{Command: "build", Engine: nil}
	err := r.RunResolution(context.Background(), res, nil)
	if !errors.Is(err, ErrNoEngine) {
		t.Errorf("got %v, want ErrNoEngine", err)
	}
	if !strings.Contains(err.Error(), "build") {
		t.Errorf("error should include command name; got %q", err.Error())
	}
}

func TestRunResolutionDelegatesToEngine(t *testing.T) {
	eng := &fakeEngine{name: "uv build"}
	r := New(Options{Stderr: &bytes.Buffer{}})
	res := capability.Resolution{Command: "build", Engine: eng}
	if err := r.RunResolution(context.Background(), res, nil); err != nil {
		t.Fatal(err)
	}
	if eng.calls != 1 {
		t.Errorf("engine should run once, got %d", eng.calls)
	}
}

func TestNewFillsInDefaults(t *testing.T) {
	r := New(Options{})
	if r.opts.Stdout == nil {
		t.Error("Stdout should default to os.Stdout")
	}
	if r.opts.Stderr == nil {
		t.Error("Stderr should default to os.Stderr")
	}
}
