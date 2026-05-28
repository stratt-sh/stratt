package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stratt-sh/stratt/internal/capability"
)

// withCwd changes into dir for the test and restores the original cwd
// on cleanup.  Required because the universal command bodies use
// os.Getwd internally; tests can't pass a directory in directly without
// changing the abstraction.
func withCwd(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

func touch(t *testing.T, root, rel string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestUniversalSpecsHaveValidNames guards against typos: every name
// in universalSpecs must be a real entry in capability.UniversalCommands.
func TestUniversalSpecsHaveValidNames(t *testing.T) {
	valid := map[string]bool{}
	for _, c := range capability.UniversalCommands {
		valid[c] = true
	}
	for _, spec := range universalSpecs {
		if !valid[spec.name] {
			t.Errorf("universalSpecs entry %q is not in capability.UniversalCommands", spec.name)
		}
	}
}

// TestUniversalSpecsHaveShortDescriptions guards against missing help text.
func TestUniversalSpecsHaveShortDescriptions(t *testing.T) {
	for _, spec := range universalSpecs {
		if spec.short == "" {
			t.Errorf("universalSpec %q has empty short description", spec.name)
		}
	}
}

// TestUniversalCmdNoEngineErrorEmpty — an empty repo running `stratt build`
// produces a helpful error naming the command and pointing at doctor.
func TestUniversalCmdNoEngineErrorEmpty(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)

	cmd := newUniversalCmd(universalSpec{name: "build", short: "Build"})
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for empty repo, got nil")
	}
	if !strings.Contains(err.Error(), "build") {
		t.Errorf("error should name the command; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "stratt doctor") {
		t.Errorf("error should suggest `stratt doctor`; got %q", err.Error())
	}
}

// TestUniversalCmdNoEngineErrorWithStacks — error message includes
// detected stacks when present but no engine matched.
func TestUniversalCmdNoEngineErrorWithStacks(t *testing.T) {
	dir := t.TempDir()
	// Detected but no `test` engine for kustomize-only repos.
	touch(t, dir, "deploy/overlays/prod/kustomization.yaml")
	withCwd(t, dir)

	cmd := newUniversalCmd(universalSpec{name: "test", short: "Test"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "kustomize") {
		t.Errorf("error should list detected stack 'kustomize'; got %q", err.Error())
	}
}

// TestUniversalCmdNoExtraArgs — universal commands take no positional
// args (per the Args: cobra.NoArgs constraint).
func TestUniversalCmdNoExtraArgs(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)

	cmd := newUniversalCmd(universalSpec{name: "build", short: "Build"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"unexpected", "args"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unexpected positional args")
	}
}
