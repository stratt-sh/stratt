package bump

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestKindFromString(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Kind
		ok   bool
	}{
		{"patch", Patch, true},
		{"minor", Minor, true},
		{"major", Major, true},
		{"PATCH", Patch, true},
		{" Major ", Major, true},
		{"", 0, false},
		{"bogus", 0, false},
	} {
		got, err := KindFromString(tc.in)
		if tc.ok && err != nil {
			t.Errorf("%q: unexpected error %v", tc.in, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%q: expected error", tc.in)
		}
		if tc.ok && got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestBumpSemver(t *testing.T) {
	cases := []struct {
		current string
		kind    Kind
		want    string
	}{
		{"1.0.0", Patch, "1.0.1"},
		{"1.0.0", Minor, "1.1.0"},
		{"1.0.0", Major, "2.0.0"},
		{"1.2.3", Patch, "1.2.4"},
		{"1.2.3", Minor, "1.3.0"},
		{"1.2.3", Major, "2.0.0"},
		{"v1.2.3", Patch, "1.2.4"},
		{"1.2.3-dev", Patch, "1.2.4"}, // prerelease dropped
	}
	for _, c := range cases {
		got, err := bumpSemver(c.current, c.kind)
		if err != nil {
			t.Errorf("%s+%v: %v", c.current, c.kind, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s+%v: got %s, want %s", c.current, c.kind, got, c.want)
		}
	}
}

func TestBumpSemverInvalid(t *testing.T) {
	for _, v := range []string{"", "1", "1.2", "1.2.3.4", "abc", "1.2.x"} {
		if _, err := bumpSemver(v, Patch); err == nil {
			t.Errorf("expected error for %q", v)
		}
	}
}

func TestIsValid(t *testing.T) {
	if !IsValid("1.2.3") {
		t.Error("1.2.3 should be valid")
	}
	if !IsValid("v1.2.3") {
		t.Error("v1.2.3 should be valid (we strip the leading v)")
	}
	if IsValid("1.2") {
		t.Error("1.2 should not be valid")
	}
}

func TestComputeFindsAndReportsChanges(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pyproject.toml", `version = "1.2.3"`)

	cfg := &Config{
		CurrentVersion: "1.2.3",
		Files: []FileEntry{
			{
				Filename: "pyproject.toml",
				Search:   `version = "{current_version}"`,
				Replace:  `version = "{new_version}"`,
			},
		},
	}
	plan, err := Compute(cfg, Patch, dir)
	if err != nil {
		t.Fatal(err)
	}
	if plan.NewVersion != "1.2.4" {
		t.Errorf("NewVersion: got %q, want 1.2.4", plan.NewVersion)
	}
	if len(plan.FileChanges) != 1 {
		t.Fatalf("FileChanges: got %d, want 1", len(plan.FileChanges))
	}
	c := plan.FileChanges[0]
	if !c.Found {
		t.Error("expected Found=true")
	}
	if c.OldChunk != `version = "1.2.3"` || c.NewChunk != `version = "1.2.4"` {
		t.Errorf("substitution wrong: old=%q new=%q", c.OldChunk, c.NewChunk)
	}
}

func TestComputeReportsMissingChunk(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pyproject.toml", `something = "else"`)

	cfg := &Config{
		CurrentVersion: "1.2.3",
		Files: []FileEntry{
			{Filename: "pyproject.toml", Search: `version = "{current_version}"`, Replace: `version = "{new_version}"`},
		},
	}
	plan, err := Compute(cfg, Patch, dir)
	if err != nil {
		t.Fatal(err)
	}
	if plan.FileChanges[0].Found {
		t.Error("expected Found=false for missing version string")
	}
}

func TestComputeAppliesTemplateDefaults(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "VERSION", "1.0.0")

	cfg := &Config{
		CurrentVersion: "1.0.0",
		// No per-file Search/Replace and no top-level template — defaults
		// should yield search="{current_version}" replace="{new_version}".
		Files: []FileEntry{{Filename: "VERSION"}},
	}
	plan, err := Compute(cfg, Patch, dir)
	if err != nil {
		t.Fatal(err)
	}
	c := plan.FileChanges[0]
	if !c.Found || c.OldChunk != "1.0.0" || c.NewChunk != "1.0.1" {
		t.Errorf("got %+v", c)
	}
}

func TestApplyWritesChanges(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pyproject.toml", `version = "1.0.0"
description = "x"`)

	cfg := &Config{
		CurrentVersion: "1.0.0",
		Files: []FileEntry{
			{Filename: "pyproject.toml",
				Search:  `version = "{current_version}"`,
				Replace: `version = "{new_version}"`},
		},
	}
	plan, err := Compute(cfg, Patch, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(plan); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
	if !strings.Contains(string(got), `version = "1.0.1"`) {
		t.Errorf("file not rewritten:\n%s", string(got))
	}
	if !strings.Contains(string(got), `description = "x"`) {
		t.Errorf("unrelated content lost:\n%s", string(got))
	}
}

func TestApplyRejectsMissingChunk(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x", "no version here")
	cfg := &Config{
		CurrentVersion: "1.0.0",
		Files:          []FileEntry{{Filename: "x"}},
	}
	plan, err := Compute(cfg, Patch, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(plan); err == nil {
		t.Fatal("expected ErrMissingVersion")
	}
}

func TestComputeCommitAndTagTemplates(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "v", "1.0.0")

	cfg := &Config{
		CurrentVersion:  "1.0.0",
		MessageTemplate: "release: {current_version} → {new_version}",
		TagNameTemplate: "release/{new_version}",
		Files:           []FileEntry{{Filename: "v"}},
	}
	plan, err := Compute(cfg, Minor, dir)
	if err != nil {
		t.Fatal(err)
	}
	if plan.CommitMessage != "release: 1.0.0 → 1.1.0" {
		t.Errorf("commit message: got %q", plan.CommitMessage)
	}
	if plan.TagName != "release/1.1.0" {
		t.Errorf("tag name: got %q", plan.TagName)
	}
}

// TestLoadFromStrattToml — native [bump] section in stratt.toml.
func TestLoadFromStrattToml(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stratt.toml", `
[bump]
current_version = "0.5.0"
[[bump.files]]
filename = "pyproject.toml"
search = "version = \"{current_version}\""
replace = "version = \"{new_version}\""
`)
	cfg, warn, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if warn != "" {
		t.Errorf("unexpected warning: %s", warn)
	}
	if cfg == nil {
		t.Fatal("expected config from stratt.toml [bump]")
	}
	if cfg.CurrentVersion != "0.5.0" {
		t.Errorf("got %q", cfg.CurrentVersion)
	}
	// User-supplied entry: pyproject.toml.
	// Auto-added entry: the source file (stratt.toml), so its
	// `[bump].current_version` stays in sync after bumps.
	if len(cfg.Files) != 2 {
		t.Fatalf("expected 2 file entries (user-supplied + auto-source); got %d: %+v",
			len(cfg.Files), cfg.Files)
	}
	if cfg.Files[0].Filename != "pyproject.toml" {
		t.Errorf("first entry should be user-supplied pyproject.toml; got %+v", cfg.Files[0])
	}
	if cfg.Files[1].Filename != "stratt.toml" {
		t.Errorf("second entry should be auto-added source (stratt.toml); got %+v", cfg.Files[1])
	}
}

// TestLoadFromPyprojectStratt — native [tool.stratt.bump] in pyproject.
func TestLoadFromPyprojectStratt(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pyproject.toml", `
[tool.stratt.bump]
current_version = "1.0.0"
[[tool.stratt.bump.files]]
filename = "VERSION"
`)
	cfg, _, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil || cfg.CurrentVersion != "1.0.0" {
		t.Errorf("got %+v", cfg)
	}
}

// TestLoadFromPyprojectBumpversion — legacy [tool.bumpversion] still works.
func TestLoadFromPyprojectBumpversion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pyproject.toml", `
[tool.bumpversion]
current_version = "1.14.1"
search = "{current_version}"
replace = "{new_version}"
allow_dirty = false
tag = true
commit = true

[[tool.bumpversion.files]]
filename = "pyproject.toml"
search = 'version = "{current_version}"'
replace = 'version = "{new_version}"'
`)
	cfg, _, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("expected bumpversion config")
	}
	if cfg.CurrentVersion != "1.14.1" {
		t.Errorf("got %q", cfg.CurrentVersion)
	}
	if !cfg.Commit || !cfg.Tag {
		t.Errorf("commit/tag flags lost: %+v", cfg)
	}
	if len(cfg.Files) != 1 {
		t.Errorf("files: %+v", cfg.Files)
	}
}

// TestLoadFromBumpversionToml — standalone .bumpversion.toml.
func TestLoadFromBumpversionToml(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".bumpversion.toml", `
current_version = "2.0.0"
[[files]]
filename = "VERSION"
`)
	cfg, _, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil || cfg.CurrentVersion != "2.0.0" {
		t.Errorf("got %+v", cfg)
	}
}

// TestLoadFromBumpversionCfgParsesNativelyButWarns — .bumpversion.cfg
// (INI) is parsed natively for back-compat with legacy bump2version
// configs, but stratt emits a deprecation note pointing at the modern
// TOML formats.
func TestLoadFromBumpversionCfgParsesNativelyButWarns(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".bumpversion.cfg", "[bumpversion]\ncurrent_version = 1.0.0\n")
	cfg, warn, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("INI cfg should produce a usable config")
	}
	if cfg.CurrentVersion != "1.0.0" {
		t.Errorf("current_version: got %q", cfg.CurrentVersion)
	}
	if !strings.Contains(warn, ".bumpversion.cfg") {
		t.Errorf("expected deprecation note; got %q", warn)
	}
}

// TestLoadPriorityOrder — when multiple locations exist, the first in
// the chain wins (R2.4.7).
func TestLoadPriorityOrder(t *testing.T) {
	dir := t.TempDir()
	// Both stratt.toml [bump] and [tool.bumpversion] in pyproject exist;
	// the native location wins.
	writeFile(t, dir, "stratt.toml", `
[bump]
current_version = "9.9.9"
`)
	writeFile(t, dir, "pyproject.toml", `
[tool.bumpversion]
current_version = "1.0.0"
`)
	cfg, _, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CurrentVersion != "9.9.9" {
		t.Errorf("native [bump] should win; got %q", cfg.CurrentVersion)
	}
}

// TestLoadAutoAddsSourceFileWhenNoFilesConfigured — even with no
// [[bump.files]] entries, the source file is auto-added so that the
// current_version field is kept in sync across releases.
func TestLoadAutoAddsSourceFileWhenNoFilesConfigured(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stratt.toml", `
[bump]
current_version = "1.0.0"
`)
	cfg, _, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Files) != 1 {
		t.Fatalf("expected auto-added stratt.toml entry; got %d: %+v",
			len(cfg.Files), cfg.Files)
	}
	if cfg.Files[0].Filename != "stratt.toml" {
		t.Errorf("auto-added entry should target source file: %+v", cfg.Files[0])
	}
}

// TestLoadDoesNotDuplicateSourceFileWhenUserListsIt — if the user has
// already added the source file to [[bump.files]], we don't auto-add
// a duplicate.
func TestLoadDoesNotDuplicateSourceFileWhenUserListsIt(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stratt.toml", `
[bump]
current_version = "1.0.0"

[[bump.files]]
filename = "stratt.toml"
search = 'current_version = "{current_version}"'
replace = 'current_version = "{new_version}"'
`)
	cfg, _, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Files) != 1 {
		t.Errorf("expected 1 entry (user-supplied), got %d: %+v",
			len(cfg.Files), cfg.Files)
	}
}

// TestLoadAutoAddsBumpversionSourceInPyproject — for legacy bumpversion
// configs in pyproject.toml, the source file (pyproject.toml) is also
// auto-added so that `[tool.bumpversion].current_version` stays in sync.
func TestLoadAutoAddsBumpversionSourceInPyproject(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pyproject.toml", `
[tool.bumpversion]
current_version = "1.0.0"
`)
	cfg, _, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range cfg.Files {
		if f.Filename == "pyproject.toml" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected pyproject.toml in auto-added Files: %+v", cfg.Files)
	}
}

// TestComputeAutoAddedSourceFileWorksEndToEnd — happy-path integration:
// loading auto-adds the source, Compute finds it, Apply rewrites it.
func TestComputeAutoAddedSourceFileWorksEndToEnd(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stratt.toml", `[bump]
current_version = "1.0.0"
`)
	cfg, _, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Compute(cfg, Patch, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(plan); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "stratt.toml"))
	if !strings.Contains(string(body), `current_version = "1.0.1"`) {
		t.Errorf("source file's current_version not updated:\n%s", body)
	}
}

// TestLoadNothingPresent — empty repo returns (nil, "", nil).
func TestLoadNothingPresent(t *testing.T) {
	cfg, warn, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg != nil {
		t.Errorf("expected nil config, got %+v", cfg)
	}
	if warn != "" {
		t.Errorf("expected no warning, got %q", warn)
	}
}

// TestLoadFromGalaxyYML — Ansible collection without any explicit bump
// config picks up galaxy.yml's version field automatically.
func TestLoadFromGalaxyYML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "galaxy.yml", "namespace: zebpalmer\nname: tailscale\nversion: 0.7.2\nreadme: README.md\n")

	cfg, _, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("expected synthesized config from galaxy.yml, got nil")
	}
	if cfg.CurrentVersion != "0.7.2" {
		t.Errorf("CurrentVersion: got %q, want %q", cfg.CurrentVersion, "0.7.2")
	}
	if len(cfg.Files) != 1 || cfg.Files[0].Filename != "galaxy.yml" {
		t.Errorf("Files: got %+v", cfg.Files)
	}
	if !cfg.Commit || !cfg.Tag {
		t.Errorf("expected Commit=Tag=true, got Commit=%v Tag=%v", cfg.Commit, cfg.Tag)
	}
}

// TestLoadFromGalaxyYMLEndToEnd — synthesized config + Compute + Apply
// actually updates galaxy.yml.
func TestLoadFromGalaxyYMLEndToEnd(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "galaxy.yml", "namespace: zebpalmer\nname: tailscale\nversion: 0.7.2\n")

	cfg, _, err := Load(dir)
	if err != nil || cfg == nil {
		t.Fatalf("load: cfg=%v err=%v", cfg, err)
	}
	plan, err := Compute(cfg, Minor, dir)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if plan.NewVersion != "0.8.0" {
		t.Errorf("NewVersion: got %q, want 0.8.0", plan.NewVersion)
	}
	if err := Apply(plan); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "galaxy.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "version: 0.8.0") {
		t.Errorf("galaxy.yml not updated; contents:\n%s", got)
	}
	if strings.Contains(string(got), "version: 0.7.2") {
		t.Errorf("old version still present:\n%s", got)
	}
}

// TestLoadGalaxyYMLPriorityBelowTOML — when both galaxy.yml and a TOML
// bump config exist, the TOML config wins.
func TestLoadGalaxyYMLPriorityBelowTOML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "galaxy.yml", "namespace: x\nname: y\nversion: 0.7.2\n")
	writeFile(t, dir, "stratt.toml", "[bump]\ncurrent_version = \"9.9.9\"\n")

	cfg, _, err := Load(dir)
	if err != nil || cfg == nil {
		t.Fatalf("load: cfg=%v err=%v", cfg, err)
	}
	if cfg.CurrentVersion != "9.9.9" {
		t.Errorf("expected TOML to win, got %q", cfg.CurrentVersion)
	}
}

// TestLoadGalaxyYMLMissingFields — a galaxy.yml without all three of
// namespace/name/version doesn't synthesize anything.
func TestLoadGalaxyYMLMissingFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "galaxy.yml", "name: y\nversion: 0.1.0\n")

	cfg, _, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg != nil {
		t.Errorf("expected nil, got %+v", cfg)
	}
}

// TestApplyReplaceAllUpdatesEveryOccurrence — replace_all=true rewrites
// every match in a file (README/changelog use case), not just the first.
func TestApplyReplaceAllUpdatesEveryOccurrence(t *testing.T) {
	dir := t.TempDir()
	body := "first version: \"1.0.0\"\nsecond version: \"1.0.0\"\n"
	writeFile(t, dir, "README.md", body)
	cfg := &Config{
		CurrentVersion: "1.0.0",
		Files: []FileEntry{{
			Filename:   "README.md",
			Search:     `version: "{current_version}"`,
			Replace:    `version: "{new_version}"`,
			ReplaceAll: true,
		}},
	}
	plan, err := Compute(cfg, Minor, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(plan); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "README.md"))
	if strings.Contains(string(got), "1.0.0") {
		t.Errorf("expected no 1.0.0 remaining, got:\n%s", got)
	}
	if strings.Count(string(got), "1.1.0") != 2 {
		t.Errorf("expected 2 occurrences of new version, got:\n%s", got)
	}
}

// TestApplyReplaceFirstOnlyByDefault — without replace_all, only the
// first match is rewritten (bump-my-version parity).
func TestApplyReplaceFirstOnlyByDefault(t *testing.T) {
	dir := t.TempDir()
	body := "version: \"1.0.0\"\nversion: \"1.0.0\"\n"
	writeFile(t, dir, "README.md", body)
	cfg := &Config{
		CurrentVersion: "1.0.0",
		Files: []FileEntry{{
			Filename: "README.md",
			Search:   `version: "{current_version}"`,
			Replace:  `version: "{new_version}"`,
		}},
	}
	plan, err := Compute(cfg, Patch, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(plan); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "README.md"))
	if strings.Count(string(got), "1.0.0") != 1 || strings.Count(string(got), "1.0.1") != 1 {
		t.Errorf("expected one of each version, got:\n%s", got)
	}
}

// TestLoadReplaceAllFromTOML — the `replace_all` field round-trips
// through the TOML loader.
func TestLoadReplaceAllFromTOML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stratt.toml", `[bump]
current_version = "1.0.0"

[[bump.files]]
filename = "README.md"
search = "v{current_version}"
replace = "v{new_version}"
replace_all = true
`)
	cfg, _, err := Load(dir)
	if err != nil || cfg == nil {
		t.Fatalf("load: cfg=%v err=%v", cfg, err)
	}
	var found *FileEntry
	for i := range cfg.Files {
		if cfg.Files[i].Filename == "README.md" {
			found = &cfg.Files[i]
		}
	}
	if found == nil {
		t.Fatal("README.md entry missing")
	}
	if !found.ReplaceAll {
		t.Errorf("ReplaceAll: got false, want true")
	}
}

func TestParseGalaxyTopLevelQuoted(t *testing.T) {
	data := []byte(`namespace: "ns"
name: 'nm'
version: 1.2.3
description: "Has a colon: yes"
`)
	got := parseGalaxyTopLevel(data)
	if got["namespace"] != "ns" || got["name"] != "nm" || got["version"] != "1.2.3" {
		t.Errorf("got %+v", got)
	}
}

func TestNextVersionPrerelease(t *testing.T) {
	cases := []struct {
		current string
		action  Action
		want    string
		wantErr bool
	}{
		// start
		{"0.17.0", Action{Kind: Minor, Op: OpStart, Label: "rc"}, "0.18.0-rc.1", false},
		{"0.17.0", Action{Kind: Patch, Op: OpStart, Label: "rc"}, "0.17.1-rc.1", false},
		{"0.17.0", Action{Kind: Major, Op: OpStart, Label: "rc"}, "1.0.0-rc.1", false},
		{"0.17.0", Action{Kind: Minor, Op: OpStart, Label: "beta"}, "0.18.0-beta.1", false},
		{"0.17.0", Action{Kind: Minor, Op: OpStart}, "0.18.0-rc.1", false}, // default label
		// start refuses when already on a prerelease
		{"0.18.0-rc.1", Action{Kind: Minor, Op: OpStart, Label: "rc"}, "", true},
		// iterate
		{"0.18.0-rc.1", Action{Op: OpIterate}, "0.18.0-rc.2", false},
		{"0.18.0-rc.9", Action{Op: OpIterate}, "0.18.0-rc.10", false},
		{"0.18.0", Action{Op: OpIterate}, "", true}, // not a prerelease
		// promote
		{"0.18.0-rc.2", Action{Op: OpPromote}, "0.18.0", false},
		{"1.0.0-beta.3", Action{Op: OpPromote}, "1.0.0", false},
		{"0.18.0", Action{Op: OpPromote}, "", true},
		// relabel (resets counter)
		{"0.18.0-rc.3", Action{Op: OpRelabel, Label: "beta"}, "0.18.0-beta.1", false},
		{"0.18.0-rc.3", Action{Op: OpRelabel}, "", true},           // missing label
		{"0.18.0", Action{Op: OpRelabel, Label: "beta"}, "", true}, // not a prerelease
		// normal release still strips suffix (back-compat)
		{"0.18.0-rc.2", Action{Kind: Patch, Op: OpRelease}, "0.18.1", false},
		{"0.17.0", Action{Kind: Minor, Op: OpRelease}, "0.18.0", false},
	}
	for _, c := range cases {
		got, err := nextVersion(c.current, c.action)
		if c.wantErr {
			if err == nil {
				t.Errorf("nextVersion(%q, %+v): expected error, got %q", c.current, c.action, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("nextVersion(%q, %+v): unexpected error %v", c.current, c.action, err)
		}
		if got != c.want {
			t.Errorf("nextVersion(%q, %+v) = %q, want %q", c.current, c.action, got, c.want)
		}
	}
}

func TestIsPrerelease(t *testing.T) {
	yes := []string{"0.18.0-rc.1", "v1.0.0-beta.10", "2.3.4-alpha.2"}
	no := []string{"0.18.0", "1.2.3-dev", "1.2.3-rc", "1.2.3-rc.0", "1.2.3+build", "garbage"}
	for _, v := range yes {
		if !IsPrerelease(v) {
			t.Errorf("IsPrerelease(%q) = false, want true", v)
		}
	}
	for _, v := range no {
		if IsPrerelease(v) {
			t.Errorf("IsPrerelease(%q) = true, want false", v)
		}
	}
}

func TestParseAction(t *testing.T) {
	cases := []struct {
		tokens []string
		pre    string
		want   Action
		ok     bool
		errs   bool
	}{
		{nil, "", Action{}, false, false}, // prompt
		{[]string{"minor"}, "", Action{Kind: Minor, Op: OpRelease}, true, false},
		{[]string{"preminor"}, "", Action{Kind: Minor, Op: OpStart, Label: "rc"}, true, false},
		{[]string{"prepatch"}, "", Action{Kind: Patch, Op: OpStart, Label: "rc"}, true, false},
		{[]string{"preminor", "beta"}, "", Action{Kind: Minor, Op: OpStart, Label: "beta"}, true, false}, // positional label
		{[]string{"minor"}, "rc", Action{Kind: Minor, Op: OpStart, Label: "rc"}, true, false},            // --pre
		{[]string{"minor"}, "beta", Action{Kind: Minor, Op: OpStart, Label: "beta"}, true, false},        // --pre=beta
		{[]string{"minor,rc"}, "", Action{Kind: Minor, Op: OpStart, Label: "rc"}, true, false},           // chained
		{[]string{"major,beta"}, "", Action{Kind: Major, Op: OpStart, Label: "beta"}, true, false},
		{[]string{"iterate"}, "", Action{Op: OpIterate}, true, false},
		{[]string{"promote"}, "", Action{Op: OpPromote}, true, false},
		{[]string{"relabel", "beta"}, "", Action{Op: OpRelabel, Label: "beta"}, true, false},
		{[]string{"relabel"}, "rc", Action{Op: OpRelabel, Label: "rc"}, true, false}, // label via --pre
		// errors
		{[]string{"relabel"}, "", Action{}, false, true},      // no label
		{[]string{"bogus"}, "", Action{}, false, true},        // unknown verb
		{[]string{"iterate"}, "rc", Action{}, false, true},    // iterate + label
		{[]string{"minor,rc"}, "beta", Action{}, false, true}, // conflicting labels
		{nil, "rc", Action{}, false, true},                    // --pre with no base
	}
	for _, c := range cases {
		got, ok, err := ParseAction(c.tokens, c.pre)
		if c.errs {
			if err == nil {
				t.Errorf("ParseAction(%v,%q): expected error, got %+v", c.tokens, c.pre, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseAction(%v,%q): unexpected error %v", c.tokens, c.pre, err)
			continue
		}
		if ok != c.ok || got != c.want {
			t.Errorf("ParseAction(%v,%q) = %+v,%v, want %+v,%v", c.tokens, c.pre, got, ok, c.want, c.ok)
		}
	}
}
