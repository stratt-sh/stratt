package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeConfig drops a stratt.toml with the given body at root.
func writeConfig(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "stratt.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// resetRepo builds a Go repo where the setup stage is overridden with an
// observable echo, so tests exercise the clean → setup chain hermetically
// (no real `go mod download`).  Returns the repo dir.
func resetRepo(t *testing.T, extraConfig string) string {
	t.Helper()
	dir := t.TempDir()
	touch(t, dir, "go.mod")
	writeConfig(t, dir, `
[tasks.setup]
run = "echo SETUP-RAN"
`+extraConfig)
	// Seed something for clean to remove so its stage is observable.
	if err := os.MkdirAll(filepath.Join(dir, ".stratt", "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestResetRunsCleanThenSetup — the two stages run through the same
// registry tasks the standalone commands use, clean first.
func TestResetRunsCleanThenSetup(t *testing.T) {
	dir := resetRepo(t, "")
	withCwd(t, dir)

	cmd := newResetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	cleanAt := strings.Index(out.String(), "removed "+filepath.Join(".stratt", "cache"))
	setupAt := strings.Index(out.String(), "SETUP-RAN")
	if cleanAt < 0 {
		t.Fatalf("clean stage output missing: %q", out.String())
	}
	if setupAt < 0 {
		t.Fatalf("setup stage output missing: %q", out.String())
	}
	if cleanAt > setupAt {
		t.Errorf("clean must run before setup; output: %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".stratt", "cache")); !os.IsNotExist(err) {
		t.Errorf(".stratt/cache should be removed: stat err=%v", err)
	}
}

// TestResetStopsWhenCleanFails — a failing clean stage aborts reset
// before setup runs (serial, fail-fast per R2.6.5).
func TestResetStopsWhenCleanFails(t *testing.T) {
	dir := resetRepo(t, `
[tasks.clean]
run = "exit 1"
`)
	withCwd(t, dir)

	cmd := newResetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected reset to fail when clean fails")
	}
	if strings.Contains(out.String(), "SETUP-RAN") {
		t.Errorf("setup must not run after clean fails; output: %q", out.String())
	}
}

// TestResetDockerReachesCleanStage — --docker flows into the clean
// stage's opt-in dangling-image prune, and the prune happens before setup.
func TestResetDockerReachesCleanStage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake docker is a POSIX shell script")
	}
	dir := resetRepo(t, "")
	touch(t, dir, "Dockerfile") // docker stack, so the prune step applies

	// Fake `docker` on PATH that records its argv.
	bin := t.TempDir()
	log := filepath.Join(bin, "docker.log")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	withCwd(t, dir)

	cmd := newResetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--docker"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("fake docker was never invoked: %v", err)
	}
	if !strings.Contains(string(got), "image prune --force") {
		t.Errorf("docker argv: got %q, want image prune --force", string(got))
	}
	pruneAt := strings.Index(out.String(), "pruned dangling docker images")
	setupAt := strings.Index(out.String(), "SETUP-RAN")
	if pruneAt < 0 || setupAt < 0 || pruneAt > setupAt {
		t.Errorf("prune must happen in the clean stage, before setup; output: %q", out.String())
	}
}

// TestResetRunnableViaRun — `stratt run reset` reaches the same registry
// task, like every other built-in.
func TestResetRunnableViaRun(t *testing.T) {
	dir := resetRepo(t, "")
	withCwd(t, dir)

	cmd := newRunCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"reset"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "SETUP-RAN") {
		t.Errorf("`stratt run reset` should chain into setup; output: %q", out.String())
	}
}

// TestResetHonorsTaskAugment — [tasks.reset] before/after hooks wrap the
// composite, consistent with other built-ins.
func TestResetHonorsTaskAugment(t *testing.T) {
	dir := resetRepo(t, `
[tasks.reset]
before = ["echo RESET-BEFORE"]
after = ["echo RESET-AFTER"]
`)
	withCwd(t, dir)

	cmd := newResetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	beforeAt := strings.Index(s, "RESET-BEFORE")
	setupAt := strings.Index(s, "SETUP-RAN")
	afterAt := strings.Index(s, "RESET-AFTER")
	if beforeAt < 0 || setupAt < 0 || afterAt < 0 {
		t.Fatalf("missing hook or stage output: %q", s)
	}
	if !(beforeAt < setupAt && setupAt < afterAt) {
		t.Errorf("want before → stages → after ordering; output: %q", s)
	}
}

// TestResetDisabledViaConfig — `[tasks.reset] enabled = false` turns the
// command off, consistent with other built-in verbs.
func TestResetDisabledViaConfig(t *testing.T) {
	dir := resetRepo(t, `
[tasks.reset]
enabled = false
`)
	withCwd(t, dir)

	cmd := newResetCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error when reset is disabled")
	}
}
