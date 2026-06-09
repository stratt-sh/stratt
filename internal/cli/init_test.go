package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stratt-sh/stratt/internal/config"
)

// runInitCmd executes `stratt init <args...>` with stdin fed from input
// (one line per prompt) and returns combined output plus any error.
func runInitCmd(t *testing.T, b BuildInfo, input string, args ...string) (string, error) {
	t.Helper()
	cmd := newInitCmd(b)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(input))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestInitYesCreatesConfigAndAgents(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)

	if _, err := runInitCmd(t, BuildInfo{Version: "dev"}, "", "--yes"); err != nil {
		t.Fatalf("init --yes: %v", err)
	}

	proj, err := config.Load(dir)
	if err != nil {
		t.Fatalf("generated config does not load: %v", err)
	}
	if proj.Source != filepath.Join(dir, "stratt.toml") {
		t.Errorf("Source = %q, want stratt.toml", proj.Source)
	}

	agents, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("AGENTS.md: %v", err)
	}
	if !hasManagedBlock(string(agents)) {
		t.Error("AGENTS.md missing the managed stratt block")
	}
}

func TestInitIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)

	if _, err := runInitCmd(t, BuildInfo{Version: "dev"}, "", "--yes"); err != nil {
		t.Fatalf("first init: %v", err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "stratt.toml"))

	out, err := runInitCmd(t, BuildInfo{Version: "dev"}, "", "--yes")
	if err != nil {
		t.Fatalf("second init: %v", err)
	}
	if !strings.Contains(out, "already present") || !strings.Contains(out, "already has a stratt block") {
		t.Errorf("re-run should report both steps already done; got:\n%s", out)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "stratt.toml"))
	if !bytes.Equal(before, after) {
		t.Error("idempotent re-run rewrote stratt.toml")
	}
}

func TestInitWritesToPyprojectWhenChosen(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)
	original := "[project]\nname = \"x\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// answers: create config? yes · use pyproject? yes · add agents? no
	if _, err := runInitCmd(t, BuildInfo{Version: "dev"}, "y\ny\nn\n"); err != nil {
		t.Fatalf("init: %v", err)
	}

	// No standalone stratt.toml; config resolves from pyproject.
	if _, err := os.Stat(filepath.Join(dir, "stratt.toml")); !os.IsNotExist(err) {
		t.Error("should not have created stratt.toml when pyproject was chosen")
	}
	py, err := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(py), original) {
		t.Errorf("existing pyproject content was not preserved:\n%s", py)
	}
	if !strings.Contains(string(py), "[tool.stratt]") {
		t.Errorf("pyproject missing [tool.stratt]:\n%s", py)
	}
	proj, err := config.Load(dir)
	if err != nil {
		t.Fatalf("pyproject config does not load: %v", err)
	}
	if filepath.Base(proj.Source) != "pyproject.toml" {
		t.Errorf("Source = %q, want pyproject.toml", proj.Source)
	}
}

func TestInitDeclineWritesNothing(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)

	// answers: create config? no · add agents? no
	if _, err := runInitCmd(t, BuildInfo{Version: "dev"}, "n\nn\n"); err != nil {
		t.Fatalf("init: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("declining every prompt should write no files; found %d", len(entries))
	}
}

func TestInitLeavesExistingConfigUntouched(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)
	existing := "required_stratt = \">= 0.5.0\"\n"
	path := filepath.Join(dir, "stratt.toml")
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runInitCmd(t, BuildInfo{Version: "1.0.0"}, "", "--yes")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out, "already present") {
		t.Errorf("should report existing config; got:\n%s", out)
	}
	got, _ := os.ReadFile(path)
	if string(got) != existing {
		t.Errorf("existing stratt.toml was modified:\n%s", got)
	}
}
