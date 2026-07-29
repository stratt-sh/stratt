package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanRemovesGoArtifacts(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "go.mod")
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "x"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	withCwd(t, dir)

	cmd := newCleanCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bin")); !os.IsNotExist(err) {
		t.Errorf("./bin should be removed: stat err=%v", err)
	}
	if !strings.Contains(out.String(), "bin") {
		t.Errorf("output should mention removed bin: %q", out.String())
	}
}

func TestCleanRemovesPythonArtifacts(t *testing.T) {
	dir := t.TempDir()
	// Point uv at an empty temp cache so `uv cache clean` (invoked by
	// the python+uv branch) doesn't walk the dev machine's real cache —
	// that walk has been observed to take several minutes on caches
	// with many cached wheels.
	t.Setenv("UV_CACHE_DIR", t.TempDir())
	touch(t, dir, "pyproject.toml")
	touch(t, dir, "uv.lock")
	if err := os.MkdirAll(filepath.Join(dir, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".pytest_cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "pkg", "__pycache__"), 0o755); err != nil {
		t.Fatal(err)
	}
	withCwd(t, dir)

	cmd := newCleanCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"dist", ".pytest_cache", "pkg/__pycache__"} {
		if _, err := os.Stat(filepath.Join(dir, p)); !os.IsNotExist(err) {
			t.Errorf("%s should be removed: stat err=%v", p, err)
		}
	}
}

func TestCleanRemovesStrattCache(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, ".stratt", "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	withCwd(t, dir)

	cmd := newCleanCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Errorf(".stratt/cache should be removed: stat err=%v", err)
	}
}

func TestCleanNoStacksStillRemovesStrattCache(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, ".stratt", "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	withCwd(t, dir)

	cmd := newCleanCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Errorf(".stratt/cache should be removed regardless of stacks: %v", err)
	}
}

// TestCleanDockerWithoutDockerStack — passing --docker in a repo with no
// docker stack is inapplicable, not an error; clean says so and proceeds.
func TestCleanDockerWithoutDockerStack(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "go.mod")
	withCwd(t, dir)

	cmd := newCleanCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--docker"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "skipped docker image prune") {
		t.Errorf("expected a skip notice for --docker without a docker stack; got %q", out.String())
	}
}

// TestCleanHonorsTaskOverride — [tasks.clean] with a run body replaces
// the built-in clean, now that clean dispatches through the registry.
func TestCleanHonorsTaskOverride(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stratt.toml"), []byte(`
[tasks.clean]
run = "echo CUSTOM-CLEAN"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	withCwd(t, dir)

	cmd := newCleanCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "CUSTOM-CLEAN") {
		t.Errorf("override should replace the built-in clean; got %q", out.String())
	}
}
