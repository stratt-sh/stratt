package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

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
