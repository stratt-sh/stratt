package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runRootHelp(t *testing.T) string {
	t.Helper()
	root := newRootCmd(BuildInfo{Version: "1.0.0", Commit: "x", Date: "x"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// TestHelpListsPublicProjectTasks — `--help` ends with a Project Tasks
// section listing public [tasks.*] with their descriptions, noting the
// source and how to run them.
func TestHelpListsPublicProjectTasks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stratt.toml", strings.Join([]string{
		`[tasks.deploydocs]`,
		`description = "Publish the docs site"`,
		`run = "echo publish"`,
		``,
		`[helpers.secret]`,
		`description = "should not appear"`,
		`run = "echo hidden"`,
	}, "\n"))
	withCwd(t, dir)

	body := runRootHelp(t)
	for _, want := range []string{
		"Project Tasks",
		"stratt.toml",
		"stratt run <task>",
		"deploydocs", "Publish the docs site",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("help missing %q\n--- output ---\n%s", want, body)
		}
	}
	// Helpers are hidden and must not leak into the public section.
	if strings.Contains(body, "secret") || strings.Contains(body, "should not appear") {
		t.Errorf("helpers must not appear in Project Tasks; got:\n%s", body)
	}
}

// TestHelpNoProjectTasksSection — with no config in the repo, help has no
// Project Tasks section and still renders the built-in commands.
func TestHelpNoProjectTasksSection(t *testing.T) {
	withCwd(t, t.TempDir())
	body := runRootHelp(t)
	if strings.Contains(body, "Project Tasks") {
		t.Errorf("expected no Project Tasks section without config; got:\n%s", body)
	}
	if !strings.Contains(body, "Available Commands") {
		t.Errorf("built-in command help should still render; got:\n%s", body)
	}
}

// TestHelpSkipsDisabledTasks — a task with enabled = false is omitted.
func TestHelpSkipsDisabledTasks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stratt.toml", strings.Join([]string{
		`[tasks.active]`,
		`description = "I am on"`,
		`run = "echo on"`,
		``,
		`[tasks.off]`,
		`description = "I am off"`,
		`run = "echo off"`,
		`enabled = false`,
	}, "\n"))
	withCwd(t, dir)

	body := runRootHelp(t)
	if !strings.Contains(body, "active") {
		t.Errorf("enabled task should appear; got:\n%s", body)
	}
	if strings.Contains(body, "I am off") {
		t.Errorf("disabled task should be omitted; got:\n%s", body)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
