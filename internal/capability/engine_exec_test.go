package capability

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecEngineNameDefault(t *testing.T) {
	e := &execEngine{tool: "uv", argv: []string{"run", "pytest"}}
	if got := e.Name(); got != "uv run pytest" {
		t.Errorf("got %q, want %q", got, "uv run pytest")
	}
}

func TestExecEngineNameOverride(t *testing.T) {
	e := &execEngine{tool: "go", argv: []string{"build", "./..."}, display: "go build ./..."}
	if got := e.Name(); got != "go build ./..." {
		t.Errorf("got %q", got)
	}
}

func TestExecEngineStatusMissing(t *testing.T) {
	// A tool name that's vanishingly unlikely to exist on PATH.
	e := &execEngine{tool: "this-binary-definitely-does-not-exist-xyzzy"}
	if got := e.Status(); got != StatusMissingTool {
		t.Errorf("got %v, want StatusMissingTool", got)
	}
}

func TestExecEngineStatusReady(t *testing.T) {
	// `go` should be on PATH in any environment that built the test
	// binary in the first place.
	e := &execEngine{tool: "go", argv: []string{"version"}}
	if got := e.Status(); got != StatusReady {
		t.Errorf("got %v, want StatusReady", got)
	}
}

// TestExecEngineRun — runs a known-safe trivial command and confirms
// success / non-zero handling.  Uses the standard `go version` because
// it's guaranteed to exist on a Go test environment.
func TestExecEngineRun(t *testing.T) {
	e := &execEngine{tool: "go", argv: []string{"version"}}
	if err := e.Run(context.Background(), nil); err != nil {
		t.Errorf("Run failed unexpectedly: %v", err)
	}
}

func TestExecEngineRunFailure(t *testing.T) {
	// `go this-subcommand-does-not-exist` returns non-zero with a real
	// error.  We expect Run to surface that.
	e := &execEngine{tool: "go", argv: []string{"this-subcommand-does-not-exist"}}
	err := e.Run(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error from `go this-subcommand-does-not-exist`, got nil")
	}
	// Error should include the engine name to be useful in stratt output.
	if !strings.Contains(err.Error(), "go this-subcommand-does-not-exist") {
		t.Errorf("error should include engine name; got %q", err.Error())
	}
}

func TestNotImplementedEngine(t *testing.T) {
	e := &notImplementedEngine{display: "future thing"}
	if e.Name() != "future thing" {
		t.Errorf("name: got %q", e.Name())
	}
	if e.Status() != StatusPending {
		t.Errorf("status: got %v", e.Status())
	}
	err := e.Run(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("got %q", err.Error())
	}
}

func TestDelegateEngine(t *testing.T) {
	e := &delegateEngine{display: "release engine", delegateCmd: "stratt release"}
	if e.Name() != "release engine" {
		t.Errorf("name: got %q", e.Name())
	}
	if e.Status() != StatusReady {
		t.Errorf("delegate engines should report Ready, got %v", e.Status())
	}
	err := e.Run(context.Background(), nil)
	if err == nil {
		t.Fatal("delegate engine Run should error")
	}
	if !strings.Contains(err.Error(), "stratt release") {
		t.Errorf("error should reference the delegate command: %q", err.Error())
	}
}

// TestFanOutEngineRunsInSubprojectDirs proves the fan-out actually changes
// working directory per subproject: each subproject writes its cwd to a file, and
// we confirm the files land in the right directories.  This is the
// correctness guarantee behind monorepo dispatch — without setEngineDir
// every subproject would run in the process cwd.
func TestFanOutEngineRunsInSubprojectDirs(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "backend")
	b := filepath.Join(root, "frontend")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	mk := func(dir string) Engine {
		e := &execEngine{tool: "sh", argv: []string{"-c", "pwd -P > marker.txt"}}
		setEngineDir(e, dir)
		return e
	}
	ws := &fanOutEngine{
		command: "test",
		subprojects: []subprojectStep{
			{dir: "backend", engine: mk(a)},
			{dir: "frontend", engine: mk(b)},
		},
	}
	if err := ws.Run(context.Background(), nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, d := range []string{a, b} {
		got, err := os.ReadFile(filepath.Join(d, "marker.txt"))
		if err != nil {
			t.Fatalf("read marker in %s: %v", d, err)
		}
		// macOS /tmp is a symlink to /private/tmp; `pwd -P` resolves it,
		// so resolve our expectation the same way before comparing.
		want, _ := filepath.EvalSymlinks(d)
		if strings.TrimSpace(string(got)) != want {
			t.Errorf("subproject ran in %q, want %q", strings.TrimSpace(string(got)), want)
		}
	}
}

// TestFanOutEngineFailFast — a failing subproject stops the run and the
// error names the command and the subproject directory.
func TestFanOutEngineFailFast(t *testing.T) {
	good := &fakeEngine{name: "ok"}
	bad := &execEngine{tool: "sh", argv: []string{"-c", "exit 3"}}
	after := &fakeEngine{name: "after"}
	ws := &fanOutEngine{
		command: "test",
		subprojects: []subprojectStep{
			{dir: "backend", engine: good},
			{dir: "frontend", engine: bad},
			{dir: "docs", engine: after},
		},
	}
	err := ws.Run(context.Background(), nil)
	if err == nil {
		t.Fatal("expected failure")
	}
	if !strings.Contains(err.Error(), "test in frontend") {
		t.Errorf("error should name the command and subproject: %q", err.Error())
	}
	if after.calls != 0 {
		t.Error("subprojects after the failure should not run (fail-fast)")
	}
}
