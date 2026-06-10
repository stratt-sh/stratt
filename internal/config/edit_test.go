package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetRequiredStrattInStrattToml(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stratt.toml")
	if err := os.WriteFile(path, []byte("# placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetRequiredStratt(path, ">= 1.5.0"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), `required_stratt = '>= 1.5.0'`) &&
		!strings.Contains(string(body), `required_stratt = ">= 1.5.0"`) {
		t.Errorf("file should contain required_stratt; got:\n%s", body)
	}
}

func TestSetRequiredStrattInPyprojectToml(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pyproject.toml")
	if err := os.WriteFile(path, []byte(`
[project]
name = "x"

[tool.stratt]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetRequiredStratt(path, ">= 2.0.0"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	// Must appear under [tool.stratt], not at top level.
	if !strings.Contains(string(body), "[tool.stratt]") {
		t.Errorf("expected [tool.stratt] section; got:\n%s", body)
	}
	if !strings.Contains(string(body), `>= 2.0.0`) {
		t.Errorf("expected required_stratt value:\n%s", body)
	}
}

// TestSetRequiredStrattPreservesComments — the edit must be line-surgical,
// leaving the user's comments and unrelated keys intact (regression: it
// used to round-trip the whole document through a TOML marshaller, wiping
// every comment).
func TestSetRequiredStrattPreservesComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stratt.toml")
	original := "# top comment\nrequired_stratt = \">= 1.0.0\"  # pin\n\n[release]\n# keep me\nbranch = \"main\"\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetRequiredStratt(path, ">= 2.0.0"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	for _, want := range []string{"# top comment", "[release]", "# keep me", `branch = "main"`, `required_stratt = ">= 2.0.0"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("missing %q after edit; got:\n%s", want, body)
		}
	}
	if strings.Contains(string(body), "1.0.0") {
		t.Errorf("old constraint should be replaced:\n%s", body)
	}
	// The replaced line should be the only required_stratt line.
	if n := strings.Count(string(body), "required_stratt"); n != 1 {
		t.Errorf("expected exactly one required_stratt line, got %d:\n%s", n, body)
	}
}

// TestSetRequiredStrattPyprojectPreservesComments — same guarantee for the
// nested [tool.stratt] case, including unrelated [tool.*] tables.
func TestSetRequiredStrattPyprojectPreservesComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pyproject.toml")
	original := "[build-system]\n# build comment\nrequires = [\"hatchling\"]\n\n[tool.stratt]\n# stratt comment\nrequired_stratt = \">= 1.0.0\"\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetRequiredStratt(path, ">= 2.0.0"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	for _, want := range []string{"[build-system]", "# build comment", `requires = ["hatchling"]`, "# stratt comment", `required_stratt = ">= 2.0.0"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("missing %q after edit; got:\n%s", want, body)
		}
	}
	if strings.Contains(string(body), "1.0.0") {
		t.Errorf("old constraint should be replaced:\n%s", body)
	}
}

func TestSetRequiredStrattRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stratt.toml")
	_ = os.WriteFile(path, []byte("# x\n"), 0o644)

	if err := SetRequiredStratt(path, ">= 1.0.0"); err != nil {
		t.Fatal(err)
	}
	proj, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if proj.RequiredStratt != ">= 1.0.0" {
		t.Errorf("got %q", proj.RequiredStratt)
	}
}
