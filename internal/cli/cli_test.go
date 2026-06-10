package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunPrintsErrorOnNonZeroExit guards against a regression where
// SilenceErrors on the root command suppressed all error output —
// users saw exit code 1 with no message.
func TestRunPrintsErrorOnNonZeroExit(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)

	// Capture stderr by swapping os.Stderr for the duration of the
	// call.  Run() writes directly to os.Stderr, so this is the
	// reliable seam.
	origStderr := os.Stderr
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = wr
	t.Cleanup(func() { os.Stderr = origStderr })

	// Argv: invoke `stratt build` against an empty dir → no engine → error.
	origArgs := os.Args
	os.Args = []string{"stratt", "build"}
	t.Cleanup(func() { os.Args = origArgs })

	exit := Run(BuildInfo{Version: "test", Commit: "none", Date: "now"})

	// Close the writer so the goroutine reading stderr returns EOF.
	wr.Close()
	stderr, _ := io.ReadAll(rd)
	os.Stderr = origStderr

	if exit != 1 {
		t.Errorf("exit code: got %d, want 1", exit)
	}
	if !strings.Contains(string(stderr), "error:") {
		t.Errorf("expected 'error:' prefix in stderr; got %q", string(stderr))
	}
	if !strings.Contains(string(stderr), "build") {
		t.Errorf("expected command name in stderr; got %q", string(stderr))
	}
}

// TestRunSucceedsOnVersion sanity-checks the success path.
func TestRunSucceedsOnVersion(t *testing.T) {
	// Capture stdout by piping.
	origStdout := os.Stdout
	rd, wr, _ := os.Pipe()
	os.Stdout = wr
	t.Cleanup(func() { os.Stdout = origStdout })

	origArgs := os.Args
	os.Args = []string{"stratt", "version"}
	t.Cleanup(func() { os.Args = origArgs })

	exit := Run(BuildInfo{Version: "9.9.9-test", Commit: "abc1234", Date: "2026-01-01"})

	wr.Close()
	stdout, _ := io.ReadAll(rd)
	os.Stdout = origStdout

	if exit != 0 {
		t.Errorf("exit code: got %d, want 0", exit)
	}
	if !strings.Contains(string(stdout), "9.9.9-test") {
		t.Errorf("expected version in stdout; got %q", string(stdout))
	}
}

// TestRequiredStrattPinBlocksOldBinary — a repo with a higher
// required_stratt than the running binary must error before reaching
// the subcommand.  R2.3.12 / R2.3.14.
func TestRequiredStrattPinBlocksOldBinary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stratt.toml"),
		[]byte("required_stratt = \">= 99.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withCwd(t, dir)

	origStderr := os.Stderr
	rd, wr, _ := os.Pipe()
	os.Stderr = wr
	t.Cleanup(func() { os.Stderr = origStderr })

	origArgs := os.Args
	os.Args = []string{"stratt", "build"}
	t.Cleanup(func() { os.Args = origArgs })

	// Binary version 1.0.0 < 99.0.0; should fail before the build runs.
	exit := Run(BuildInfo{Version: "1.0.0", Commit: "abc", Date: "now"})

	wr.Close()
	stderr, _ := io.ReadAll(rd)
	os.Stderr = origStderr

	if exit != 1 {
		t.Errorf("exit: got %d, want 1", exit)
	}
	if !strings.Contains(string(stderr), ">= 99.0.0") {
		t.Errorf("stderr should cite required constraint; got %q", string(stderr))
	}
}

// TestRequiredStrattPinAllowsCurrentBinary — same setup but a satisfying
// binary version should proceed past the pin (and then hit the empty
// "no engine matched" or "go test ..." path depending on stack).
func TestRequiredStrattPinAllowsCurrentBinary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stratt.toml"),
		[]byte("required_stratt = \">= 0.0.1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withCwd(t, dir)

	origStderr := os.Stderr
	rd, wr, _ := os.Pipe()
	os.Stderr = wr
	t.Cleanup(func() { os.Stderr = origStderr })

	origArgs := os.Args
	os.Args = []string{"stratt", "build"}
	t.Cleanup(func() { os.Args = origArgs })

	exit := Run(BuildInfo{Version: "1.0.0", Commit: "abc", Date: "now"})

	wr.Close()
	stderr, _ := io.ReadAll(rd)
	os.Stderr = origStderr

	// We expect failure because no engine matched, but NOT because of
	// the version pin.  The stderr should mention "no engine matched"
	// rather than ">= 0.0.1".
	if exit == 0 {
		t.Error("empty repo should still fail at the no-engine step")
	}
	if strings.Contains(string(stderr), ">= 0.0.1") {
		t.Errorf("pin should pass silently; got %q", string(stderr))
	}
}

// TestRequiredStrattPinSkippedForVersionAndDoctor — users must be able
// to introspect a repo whose pin they don't satisfy.  `version` and
// `doctor` are exempt per the PersistentPreRunE skiplist.
func TestRequiredStrattPinSkippedForVersionAndDoctor(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stratt.toml"),
		[]byte("required_stratt = \">= 99.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withCwd(t, dir)

	for _, sub := range []string{"version", "doctor"} {
		origStdout := os.Stdout
		rd, wr, _ := os.Pipe()
		os.Stdout = wr

		origArgs := os.Args
		os.Args = []string{"stratt", sub}

		exit := Run(BuildInfo{Version: "1.0.0", Commit: "abc", Date: "now"})

		wr.Close()
		out, _ := io.ReadAll(rd)
		os.Stdout = origStdout
		os.Args = origArgs

		if exit != 0 {
			t.Errorf("%s: exit got %d, want 0; output=%q", sub, exit, out)
		}
	}
}

// TestSelfBypassesBrokenProjectConfig — `self check` and `self update`
// must run even when stratt.toml is malformed.  These are the commands
// users reach for when something's broken; gating them on healthy
// project config would defeat their purpose.
func TestSelfBypassesBrokenProjectConfig(t *testing.T) {
	dir := t.TempDir()
	// `bogus_field` triggers DisallowUnknownFields in the strict loader.
	if err := os.WriteFile(filepath.Join(dir, "stratt.toml"),
		[]byte("bogus_field = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withCwd(t, dir)

	// `self --help` exercises the same PersistentPreRunE exemption
	// path as `self check` / `self update` without hitting the
	// network (the live `self check` calls the GitHub releases API,
	// which is rate-limited and flakes CI).
	for _, args := range [][]string{
		{"stratt", "self", "--help"},
	} {
		origStderr := os.Stderr
		rd, wr, _ := os.Pipe()
		os.Stderr = wr

		origArgs := os.Args
		os.Args = args

		exit := Run(BuildInfo{Version: "1.0.0", Commit: "abc", Date: "now"})

		wr.Close()
		stderr, _ := io.ReadAll(rd)
		os.Stderr = origStderr
		os.Args = origArgs

		if exit != 0 {
			t.Errorf("%v: exit got %d, want 0 (stderr=%q)", args, exit, stderr)
		}
		if strings.Contains(string(stderr), "unknown configuration key") {
			t.Errorf("%v: project-config error leaked through; stderr=%q", args, stderr)
		}
	}
}

// TestNonExemptCommandStillRespectsBrokenConfig — the inverse: a real
// command like `build` must still fail on a malformed config.  Guards
// against accidentally widening the exemption list.
func TestNonExemptCommandStillRespectsBrokenConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stratt.toml"),
		[]byte("bogus_field = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withCwd(t, dir)

	origStderr := os.Stderr
	rd, wr, _ := os.Pipe()
	os.Stderr = wr
	t.Cleanup(func() { os.Stderr = origStderr })

	origArgs := os.Args
	os.Args = []string{"stratt", "build"}
	t.Cleanup(func() { os.Args = origArgs })

	exit := Run(BuildInfo{Version: "1.0.0", Commit: "abc", Date: "now"})
	wr.Close()
	stderr, _ := io.ReadAll(rd)

	if exit == 0 {
		t.Errorf("expected non-zero exit on malformed config, got 0; stderr=%q", stderr)
	}
	if !strings.Contains(string(stderr), "unknown configuration key") {
		t.Errorf("expected unknown-key error message; got %q", stderr)
	}
}

// TestRunBuildsRootCommand confirms the root command tree wires together
// successfully — picks up if a future change forgets to wire a command.
func TestRunBuildsRootCommand(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "test"})
	subs := root.Commands()

	expected := map[string]bool{
		"version": false,
		"doctor":  false,
		"build":   false,
		"test":    false,
		"lint":    false,
		"format":  false,
		"setup":   false,
		"sync":    false,
		"lock":    false,
		"upgrade": false,
	}
	for _, c := range subs {
		if _, ok := expected[c.Name()]; ok {
			expected[c.Name()] = true
		}
	}
	for name, seen := range expected {
		if !seen {
			t.Errorf("expected subcommand %q to be registered on root", name)
		}
	}

	// `--help` rendering should not panic or return error.
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Errorf("--help should not error: %v", err)
	}
}
