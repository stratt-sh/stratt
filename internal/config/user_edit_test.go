package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetUserWorkspaceCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "config.toml")

	if err := SetUserWorkspace(path, "~/code", "{host}/{org}/{repo}"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	want := "[workspace]\nroot = \"~/code\"\nlayout = \"{host}/{org}/{repo}\"\n"
	if got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

func TestSetUserWorkspaceAppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	preserved := "# my comment\n[display]\ncolor = \"always\"\n"
	if err := os.WriteFile(path, []byte(preserved), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SetUserWorkspace(path, "/tmp/code", "{org}/{repo}"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	got := string(body)
	if !strings.Contains(got, preserved) {
		t.Errorf("existing content not preserved; got:\n%s", got)
	}
	if !strings.Contains(got, "[workspace]") {
		t.Errorf("workspace section not appended; got:\n%s", got)
	}
	if !strings.Contains(got, "layout = \"{org}/{repo}\"") {
		t.Errorf("layout missing; got:\n%s", got)
	}
}

func TestSetUserWorkspaceRoundTripsThroughLoadUser(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	t.Setenv("STRATT_CONFIG", path)

	if err := SetUserWorkspace(path, "~/code", "{org}/{repo}"); err != nil {
		t.Fatal(err)
	}
	usr, err := LoadUser()
	if err != nil {
		t.Fatal(err)
	}
	if usr.Workspace == nil {
		t.Fatal("Workspace is nil")
	}
	if usr.Workspace.Root != "~/code" || usr.Workspace.Layout != "{org}/{repo}" {
		t.Errorf("Workspace = %+v", usr.Workspace)
	}
}

func TestSetUserWorkspaceProtocolInsertsIntoExistingSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := SetUserWorkspace(path, "~/code", "{host}/{org}/{repo}"); err != nil {
		t.Fatal(err)
	}
	if err := SetUserWorkspaceProtocol(path, "ssh"); err != nil {
		t.Fatal(err)
	}

	body, _ := os.ReadFile(path)
	got := string(body)
	if !strings.Contains(got, "protocol = \"ssh\"") {
		t.Errorf("protocol not written; got:\n%s", got)
	}
	// Only one [workspace] header — protocol joined the existing section.
	if n := strings.Count(got, "[workspace]"); n != 1 {
		t.Errorf("expected exactly one [workspace] section, got %d:\n%s", n, got)
	}
	if !strings.Contains(got, "root = \"~/code\"") {
		t.Errorf("root not preserved; got:\n%s", got)
	}
}

func TestSetUserWorkspaceProtocolReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	initial := "[workspace]\nroot = \"~/code\"\nprotocol = \"https\"\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetUserWorkspaceProtocol(path, "ssh"); err != nil {
		t.Fatal(err)
	}

	got := mustRead(t, path)
	if strings.Contains(got, "https") {
		t.Errorf("old protocol not replaced; got:\n%s", got)
	}
	if strings.Count(got, "protocol") != 1 {
		t.Errorf("expected a single protocol key; got:\n%s", got)
	}
	if !strings.Contains(got, "protocol = \"ssh\"") {
		t.Errorf("new protocol missing; got:\n%s", got)
	}
}

func TestSetUserWorkspaceProtocolAppendsWhenNoSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	preserved := "[display]\ncolor = \"always\"\n"
	if err := os.WriteFile(path, []byte(preserved), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetUserWorkspaceProtocol(path, "https"); err != nil {
		t.Fatal(err)
	}

	got := mustRead(t, path)
	if !strings.Contains(got, preserved) {
		t.Errorf("existing content not preserved; got:\n%s", got)
	}
	if !strings.Contains(got, "[workspace]\nprotocol = \"https\"") {
		t.Errorf("workspace/protocol not appended; got:\n%s", got)
	}
}

func TestSetUserWorkspaceProtocolRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	t.Setenv("STRATT_CONFIG", path)

	if err := SetUserWorkspace(path, "~/code", "{org}/{repo}"); err != nil {
		t.Fatal(err)
	}
	if err := SetUserWorkspaceProtocol(path, "ssh"); err != nil {
		t.Fatal(err)
	}
	usr, err := LoadUser()
	if err != nil {
		t.Fatal(err)
	}
	if usr.Workspace == nil || usr.Workspace.Protocol != "ssh" {
		t.Errorf("Workspace = %+v, want Protocol=ssh", usr.Workspace)
	}
	if usr.Workspace.Root != "~/code" || usr.Workspace.Layout != "{org}/{repo}" {
		t.Errorf("root/layout not preserved: %+v", usr.Workspace)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
