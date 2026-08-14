package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stratt-sh/stratt/internal/ui"
)

// plainStyle is a color-free style for exercising helpers that print.
func plainStyle() *ui.Style {
	return ui.NewStyle(io.Discard, io.Discard, ui.ColorNever, ui.Normal)
}

// TestAgentsContextIncludesOrientationAndMap — `agents context` prints
// the static orientation and the live resolved command map for the repo.
func TestAgentsContextIncludesOrientationAndMap(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "go.mod")
	withCwd(t, dir)

	cmd := newAgentsContextCmd(BuildInfo{Version: "1.0.0", Commit: "abc", Date: "today"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	body := out.String()
	for _, expected := range []string{
		"stratt — agent orientation", // static prose
		"THE ONE RULE",
		"THIS REPOSITORY", // dynamic section
		"detected stacks: go (via go.mod)",
		"test", "go test ./...", // resolved map row
		"full health detail: `stratt doctor`",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("agents context missing %q\n--- full output ---\n%s", expected, body)
		}
	}
}

// TestAgentsContextWorkspaceSectionGated — the WORKSPACE section appears
// only when the user has a [workspace] root configured.
func TestAgentsContextWorkspaceSectionGated(t *testing.T) {
	run := func(t *testing.T) string {
		dir := t.TempDir()
		touch(t, dir, "go.mod")
		withCwd(t, dir)
		cmd := newAgentsContextCmd(BuildInfo{Version: "1.0.0", Commit: "abc", Date: "today"})
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&bytes.Buffer{})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	t.Run("absent without workspace config", func(t *testing.T) {
		withUserConfig(t, "") // valid but empty user config: no [workspace]
		if body := run(t); strings.Contains(body, "WORKSPACE") {
			t.Errorf("WORKSPACE section should be absent without config; got:\n%s", body)
		}
	})

	t.Run("present with workspace config", func(t *testing.T) {
		withUserConfig(t, "[workspace]\nroot = \"~/code\"\nlayout = \"{org}/{repo}\"\n")
		body := run(t)
		for _, want := range []string{"WORKSPACE", "~/code", "{org}/{repo}", "stratt workspace list"} {
			if !strings.Contains(body, want) {
				t.Errorf("WORKSPACE section missing %q; got:\n%s", want, body)
			}
		}
	})
}

// TestAgentsContextEmptyRepo — outside a recognized stack, context still
// prints orientation and notes zero-config mode rather than erroring.
func TestAgentsContextEmptyRepo(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)

	cmd := newAgentsContextCmd(BuildInfo{Version: "x", Commit: "x", Date: "x"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if !strings.Contains(body, "stratt — agent orientation") {
		t.Errorf("expected orientation even in empty repo; got:\n%s", body)
	}
	if !strings.Contains(body, "no recognized stacks") {
		t.Errorf("empty repo should note zero-config mode; got:\n%s", body)
	}
}

func TestUpsertManagedBlock(t *testing.T) {
	t.Run("append to existing content", func(t *testing.T) {
		in := "# Project\n\nGuidance.\n"
		got, existed := upsertManagedBlock(in)
		if existed {
			t.Fatal("expected existed=false for fresh content")
		}
		if !strings.HasPrefix(got, in) {
			t.Errorf("original content not preserved as prefix:\n%s", got)
		}
		if !hasManagedBlock(got) {
			t.Error("result should contain a managed block")
		}
	})

	t.Run("empty content", func(t *testing.T) {
		got, existed := upsertManagedBlock("")
		if existed {
			t.Fatal("expected existed=false for empty content")
		}
		if !hasManagedBlock(got) {
			t.Error("result should contain a managed block")
		}
	})

	t.Run("replace in place, preserve surroundings", func(t *testing.T) {
		in := "# Top\n\n" + agentsBeginMarker + "\nSTALE\n" + agentsEndMarker + "\n\n## Trailing\nkeep me\n"
		got, existed := upsertManagedBlock(in)
		if !existed {
			t.Fatal("expected existed=true when a block is present")
		}
		if strings.Contains(got, "STALE") {
			t.Error("stale block body should be replaced")
		}
		if !strings.Contains(got, "# Top") || !strings.Contains(got, "## Trailing\nkeep me") {
			t.Errorf("surrounding content must be preserved:\n%s", got)
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		once, _ := upsertManagedBlock("# x\n")
		twice, _ := upsertManagedBlock(once)
		if once != twice {
			t.Errorf("upsert should be idempotent;\nonce:\n%s\ntwice:\n%s", once, twice)
		}
	})
}

// TestSyncAgentsBlockIfStale covers the `stratt all` courtesy refresh:
// it fixes a stale block, is quiet+no-op when the block is current, and
// never creates a file or touches a repo that has no block.
func TestSyncAgentsBlockIfStale(t *testing.T) {
	// Ensure the helper's CI guard doesn't turn the mutating cases into
	// no-ops when the suite itself happens to run under CI.
	notCI := func(t *testing.T) {
		t.Setenv("CI", "")
		t.Setenv("GITHUB_ACTIONS", "")
	}

	t.Run("refreshes a stale block", func(t *testing.T) {
		notCI(t)
		dir := t.TempDir()
		path := agentsFilePath(dir)
		stale := "# Guide\n\n" + agentsBeginMarker + "\nSTALE BODY\n" + agentsEndMarker + "\n"
		if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
			t.Fatal(err)
		}

		var out bytes.Buffer
		if err := syncAgentsBlockIfStale(dir, &out, plainStyle()); err != nil {
			t.Fatalf("sync errored: %v", err)
		}
		got, _ := os.ReadFile(path)
		if strings.Contains(string(got), "STALE BODY") {
			t.Errorf("stale block should have been rewritten:\n%s", got)
		}
		if !strings.Contains(string(got), "# Guide") {
			t.Errorf("surrounding content must be preserved:\n%s", got)
		}
		if !strings.Contains(out.String(), "refreshed") {
			t.Errorf("expected a refresh notice; got: %q", out.String())
		}
	})

	t.Run("skips in CI, leaving a stale block untouched", func(t *testing.T) {
		t.Setenv("CI", "true")
		dir := t.TempDir()
		path := agentsFilePath(dir)
		stale := "# Guide\n\n" + agentsBeginMarker + "\nSTALE BODY\n" + agentsEndMarker + "\n"
		if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
			t.Fatal(err)
		}

		var out bytes.Buffer
		if err := syncAgentsBlockIfStale(dir, &out, plainStyle()); err != nil {
			t.Fatalf("sync errored: %v", err)
		}
		after, _ := os.ReadFile(path)
		if string(after) != stale {
			t.Errorf("CI run must not rewrite the block:\n%s", after)
		}
		if out.String() != "" {
			t.Errorf("should be silent in CI; got: %q", out.String())
		}
	})

	t.Run("no-op and silent when current", func(t *testing.T) {
		notCI(t)
		dir := t.TempDir()
		path := agentsFilePath(dir)
		current, _ := upsertManagedBlock("# Guide\n")
		if err := os.WriteFile(path, []byte(current), 0o644); err != nil {
			t.Fatal(err)
		}

		var out bytes.Buffer
		if err := syncAgentsBlockIfStale(dir, &out, plainStyle()); err != nil {
			t.Fatalf("sync errored: %v", err)
		}
		after, _ := os.ReadFile(path)
		if string(after) != current {
			t.Errorf("current block should be untouched:\n%s", after)
		}
		if out.String() != "" {
			t.Errorf("should be silent when up to date; got: %q", out.String())
		}
	})

	t.Run("does not create a missing file", func(t *testing.T) {
		notCI(t)
		dir := t.TempDir()
		var out bytes.Buffer
		if err := syncAgentsBlockIfStale(dir, &out, plainStyle()); err != nil {
			t.Fatalf("sync errored: %v", err)
		}
		if _, err := os.Stat(agentsFilePath(dir)); !os.IsNotExist(err) {
			t.Errorf("AGENTS.md must not be created when absent (stat err: %v)", err)
		}
	})

	t.Run("ignores a file with no managed block", func(t *testing.T) {
		notCI(t)
		dir := t.TempDir()
		path := agentsFilePath(dir)
		plain := "# Hand-written guide, no stratt block\n"
		if err := os.WriteFile(path, []byte(plain), 0o644); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := syncAgentsBlockIfStale(dir, &out, plainStyle()); err != nil {
			t.Fatalf("sync errored: %v", err)
		}
		after, _ := os.ReadFile(path)
		if string(after) != plain {
			t.Errorf("a file without a block must be left untouched:\n%s", after)
		}
	})
}

func TestAgentsInitAndSync(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)

	// run returns stdout/stderr plus any error (the root sets
	// SilenceErrors, so failures surface as the returned error).
	run := func(args ...string) (string, error) {
		root := newRootCmd(BuildInfo{Version: "x", Commit: "x", Date: "x"})
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		err := root.Execute()
		return out.String(), err
	}

	// sync before init → error pointing to init.
	if _, err := run("agents", "sync"); err == nil || !strings.Contains(err.Error(), "init") {
		t.Errorf("sync with no AGENTS.md should error pointing to init; got: %v", err)
	}

	// init creates the file.
	if _, err := run("agents", "init"); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	data, err := os.ReadFile(agentsFilePath(dir))
	if err != nil {
		t.Fatalf("init should create AGENTS.md: %v", err)
	}
	if !hasManagedBlock(string(data)) {
		t.Errorf("AGENTS.md should contain a managed block after init:\n%s", data)
	}

	// init again is a no-op (message mentions sync).
	if got, _ := run("agents", "init"); !strings.Contains(got, "already has a stratt block") {
		t.Errorf("second init should be a no-op; got: %q", got)
	}

	// sync now succeeds.
	if got, err := run("agents", "sync"); err != nil ||
		(!strings.Contains(strings.ToLower(got), "up to date") && !strings.Contains(got, "refreshed")) {
		t.Errorf("sync after init should succeed; got: %q err: %v", got, err)
	}
}
