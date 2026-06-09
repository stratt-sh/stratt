package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stratt-sh/stratt/internal/config"
)

// runConfigInitCmd executes `stratt config init <args...>` against the
// current working directory and returns combined stdout plus any error.
func runConfigInitCmd(t *testing.T, b BuildInfo, args ...string) (string, error) {
	t.Helper()
	cmd := newConfigCmd(b)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"init"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func TestConfigInitCreatesParsableConfig(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)

	if _, err := runConfigInitCmd(t, BuildInfo{Version: "dev"}); err != nil {
		t.Fatalf("init: %v", err)
	}

	path := filepath.Join(dir, "stratt.toml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stratt.toml not written: %v", err)
	}

	// The whole point of a strict-parsing config: the generated file must
	// load cleanly, not just exist.
	proj, err := config.Load(dir)
	if err != nil {
		t.Fatalf("generated config does not load: %v", err)
	}
	if proj.Source != path {
		t.Errorf("Source = %q, want %q", proj.Source, path)
	}
}

func TestConfigInitRefusesExistingWithoutForce(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)

	if _, err := runConfigInitCmd(t, BuildInfo{Version: "dev"}); err != nil {
		t.Fatalf("first init: %v", err)
	}
	if _, err := runConfigInitCmd(t, BuildInfo{Version: "dev"}); err == nil {
		t.Fatal("second init without --force should error")
	}
	if _, err := runConfigInitCmd(t, BuildInfo{Version: "dev"}, "--force"); err != nil {
		t.Fatalf("init --force should overwrite: %v", err)
	}
}

func TestConfigInitRefusesPyprojectStratt(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"),
		[]byte("[tool.stratt]\nrequired_stratt = \">= 0.1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runConfigInitCmd(t, BuildInfo{Version: "dev"})
	if err == nil {
		t.Fatal("init should refuse when [tool.stratt] lives in pyproject.toml")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "stratt.toml")); !os.IsNotExist(statErr) {
		t.Error("stratt.toml should not have been created on conflict")
	}
	if !strings.Contains(out+err.Error(), "pyproject.toml") {
		t.Errorf("error should point at pyproject.toml: out=%q err=%v", out, err)
	}
}

func TestDefaultConfigTemplatePinsVersion(t *testing.T) {
	// Real build → required_stratt is active and pinned.
	got := defaultConfigTemplate(BuildInfo{Version: "1.2.3"})
	if !strings.Contains(got, `required_stratt = ">= 1.2.3"`) {
		t.Errorf("release build should pin required_stratt; got:\n%s", got)
	}

	// Dev build → the pin is commented so the file doesn't hard-pin junk.
	dev := defaultConfigTemplate(BuildInfo{Version: "dev"})
	if strings.Contains(dev, "\nrequired_stratt =") {
		t.Errorf("dev build should leave required_stratt commented; got:\n%s", dev)
	}
}
