package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadNoConfigFiles(t *testing.T) {
	p, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("expected no error for empty repo, got %v", err)
	}
	if p == nil {
		t.Fatal("expected zero-value Project, got nil")
	}
	if p.Source != "" {
		t.Errorf("Source should be empty for zero-config, got %q", p.Source)
	}
}

func TestLoadStrattTomlMinimal(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "stratt.toml", `
required_stratt = ">= 0.1"

[tasks.deploy-staging]
description = "Deploy to staging"
run = "kubectl apply -k deploy/overlays/staging"
`)
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.RequiredStratt != ">= 0.1" {
		t.Errorf("required_stratt: got %q", p.RequiredStratt)
	}
	got, ok := p.Tasks["deploy-staging"]
	if !ok {
		t.Fatal("deploy-staging task missing")
	}
	if got.Description != "Deploy to staging" {
		t.Errorf("description: got %q", got.Description)
	}
	if !reflect.DeepEqual(got.Run, []string{"kubectl apply -k deploy/overlays/staging"}) {
		t.Errorf("run: got %v", got.Run)
	}
	if !got.Enabled {
		t.Error("enabled should default to true")
	}
}

func TestLoadStrattTomlRunAsList(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "stratt.toml", `
[tasks.release-prod]
run = ["./scripts/preflight.sh", "git push origin main"]
`)
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := p.Tasks["release-prod"].Run
	want := []string{"./scripts/preflight.sh", "git push origin main"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestLoadStrattTomlHelpers(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "stratt.toml", `
[helpers.preflight]
tasks = ["ruff", "test", "lint"]

[helpers.notify]
run = "./scripts/notify.sh"
`)
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Helpers["preflight"]; !ok {
		t.Error("preflight helper missing")
	}
	if _, ok := p.Helpers["notify"]; !ok {
		t.Error("notify helper missing")
	}
	if got := p.Helpers["preflight"].Tasks; !reflect.DeepEqual(got, []string{"ruff", "test", "lint"}) {
		t.Errorf("preflight.tasks: got %v", got)
	}
}

func TestLoadDuplicateAcrossTasksAndHelpers(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "stratt.toml", `
[tasks.shared]
run = "echo public"

[helpers.shared]
run = "echo private"
`)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected duplicate-name error, got nil")
	}
	if !strings.Contains(err.Error(), "shared") {
		t.Errorf("error should name the conflicting task: %v", err)
	}
}

func TestLoadEnabledFalseDisablesTask(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "stratt.toml", `
[tasks.docs]
enabled = false
`)
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Tasks["docs"].Enabled {
		t.Error("expected enabled=false to disable")
	}
}

func TestLoadBeforeAfterAugment(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "stratt.toml", `
[tasks.test]
before = ["docker compose up -d"]
after  = ["docker compose down"]
`)
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := p.Tasks["test"]
	if len(got.Run) != 0 {
		t.Error("run should be empty in augment mode")
	}
	if !reflect.DeepEqual(got.Before, []string{"docker compose up -d"}) {
		t.Errorf("before: got %v", got.Before)
	}
	if !reflect.DeepEqual(got.After, []string{"docker compose down"}) {
		t.Errorf("after: got %v", got.After)
	}
}

// TestLoadReleaseSection — [release] populates Project.Release.
func TestLoadReleaseSection(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "stratt.toml", `
[release]
branch = "master"
push = false
remote = "upstream"
`)
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Release == nil {
		t.Fatal("expected Release config")
	}
	if p.Release.Branch != "master" {
		t.Errorf("branch: got %q", p.Release.Branch)
	}
	if p.Release.Push == nil || *p.Release.Push {
		t.Errorf("push: expected false, got %v", p.Release.Push)
	}
	if p.Release.Remote != "upstream" {
		t.Errorf("remote: got %q", p.Release.Remote)
	}
}

// TestLoadReleasePushTrueExplicit — push=true with an explicit setting
// should populate the pointer (distinguishing it from "not set").
func TestLoadReleasePushTrueExplicit(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "stratt.toml", `
[release]
push = true
`)
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Release == nil || p.Release.Push == nil || !*p.Release.Push {
		t.Errorf("expected push=true; got %+v", p.Release)
	}
}

func TestLoadDeploySection(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "stratt.toml", `
[deploy]
primary_image = "app"
commit = true
`)
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Deploy == nil {
		t.Fatal("expected Deploy config")
	}
	if p.Deploy.PrimaryImage != "app" {
		t.Errorf("primary_image: got %q", p.Deploy.PrimaryImage)
	}
	if p.Deploy.Commit == nil || !*p.Deploy.Commit {
		t.Errorf("commit: expected true, got %v", p.Deploy.Commit)
	}
}

func TestLoadBumpSection(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "stratt.toml", `
[bump]
current_version = "1.14.1"
files = ["VERSION", "src/version.go"]
tag_prefix = "v"
`)
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Bump == nil {
		t.Fatal("expected bump config, got nil")
	}
	if p.Bump.CurrentVersion != "1.14.1" {
		t.Errorf("got %q", p.Bump.CurrentVersion)
	}
	if !reflect.DeepEqual(p.Bump.Files, []string{"VERSION", "src/version.go"}) {
		t.Errorf("files: got %v", p.Bump.Files)
	}
}

func TestLoadStrictUnknownFieldFailsAtRoot(t *testing.T) {
	dir := t.TempDir()
	// `descriotion` is a typo of `description`.  Strict parsing must
	// reject it (R2.3.8).
	write(t, dir, "stratt.toml", `
[tasks.test]
descriotion = "typo'd"
`)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
}

func TestLoadPyprojectStratt(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pyproject.toml", `
[build-system]
requires = ["hatchling"]

[tool.uv]
managed = true

[tool.bumpversion]
current_version = "0.0.1"

[tool.stratt]
required_stratt = ">= 0.5"

[tool.stratt.tasks.staging-deploy]
description = "Roll staging"
run = "kubectl apply -k deploy/overlays/staging"
`)
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.RequiredStratt != ">= 0.5" {
		t.Errorf("required_stratt: got %q", p.RequiredStratt)
	}
	if _, ok := p.Tasks["staging-deploy"]; !ok {
		t.Error("staging-deploy task missing from [tool.stratt.tasks]")
	}
}

// TestLoadPyprojectStratt_OtherToolsAreIgnored — having [tool.uv],
// [tool.ruff], [tool.bumpversion] etc. alongside [tool.stratt] must not
// fail parsing.  Strictness is scoped to the stratt namespace only.
func TestLoadPyprojectStrattOtherToolsIgnored(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pyproject.toml", `
[tool.ruff]
line-length = 100

[tool.ruff.lint]
select = ["E", "F"]

[tool.uv.workspace]
members = ["foo"]

[tool.stratt]
required_stratt = ">= 1.0"
`)
	p, err := Load(dir)
	if err != nil {
		t.Fatalf("other [tool.X] sections should not break parsing: %v", err)
	}
	if p.RequiredStratt != ">= 1.0" {
		t.Errorf("got %q", p.RequiredStratt)
	}
}

// TestLoadPyprojectStratt_UnknownStrattFieldRejected — strictness IS
// enforced inside [tool.stratt].
func TestLoadPyprojectStrattUnknownStrattFieldRejected(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pyproject.toml", `
[tool.stratt]
required_stratt = ">= 1.0"
not_a_real_field = "boom"
`)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected strict-mode error for unknown [tool.stratt] field")
	}
}

func TestLoadConflictBothFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "stratt.toml", "required_stratt = \">= 1.0\"\n")
	write(t, dir, "pyproject.toml", `
[tool.stratt]
required_stratt = ">= 1.0"
`)
	_, err := Load(dir)
	if !errors.Is(err, ErrConflict) {
		t.Errorf("expected ErrConflict, got %v", err)
	}
}

// TestLoadPyprojectWithoutStratt — pyproject.toml is allowed to exist
// with no [tool.stratt] section.  That's the most common case for
// Python repos before they adopt stratt.
func TestLoadPyprojectWithoutStratt(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pyproject.toml", `
[tool.bumpversion]
current_version = "1.0.0"

[tool.ruff]
line-length = 100
`)
	p, err := Load(dir)
	if err != nil {
		t.Fatalf("pyproject without [tool.stratt] should not error: %v", err)
	}
	if p.Source != "" {
		t.Errorf("Source should be empty when no stratt config present, got %q", p.Source)
	}
}

func TestLoadInvalidRunType(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "stratt.toml", `
[tasks.test]
run = 42
`)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for non-string run value")
	}
}

func TestLoadInvalidRunListElement(t *testing.T) {
	dir := t.TempDir()
	// TOML arrays are homogeneous, so this should also fail; but if a
	// future TOML variant allows mixed arrays, our list-element check
	// should still catch non-string entries.
	write(t, dir, "stratt.toml", `
[tasks.test]
run = ["echo ok"]
`)
	_, err := Load(dir)
	if err != nil {
		t.Errorf("homogeneous string list should parse cleanly: %v", err)
	}
}

// TestLoadSubprojectsDeclared — [[subprojects]] parses into Project.Subprojects with
// path and the optional `only` verb filter, in declared order.
func TestLoadSubprojectsDeclared(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "stratt.toml", `
[[subprojects]]
path = "backend"

[[subprojects]]
path = "frontend"
only = ["test", "lint"]
`)
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Subprojects) != 2 {
		t.Fatalf("expected 2 stacks, got %d", len(p.Subprojects))
	}
	if p.Subprojects[0].Path != "backend" || len(p.Subprojects[0].Only) != 0 {
		t.Errorf("stacks[0]: got %+v", p.Subprojects[0])
	}
	if p.Subprojects[1].Path != "frontend" {
		t.Errorf("stacks[1].Path: got %q", p.Subprojects[1].Path)
	}
	if got := p.Subprojects[1].Only; len(got) != 2 || got[0] != "test" || got[1] != "lint" {
		t.Errorf("stacks[1].Only: got %v", got)
	}
}

// TestLoadSubprojectsRequiresPath — a [[subprojects]] entry with no path is a load
// error (consistent with stratt's loud-failure stance on bad config).
func TestLoadSubprojectsRequiresPath(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "stratt.toml", `
[[subprojects]]
only = ["test"]
`)
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error for stacks entry without path")
	}
}

// TestLoadSubprojectsRejectsUnknownField — strict parsing extends into the
// [[subprojects]] table: a typo'd key fails at load time.
func TestLoadSubprojectsRejectsUnknownField(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "stratt.toml", `
[[subprojects]]
path = "backend"
onlyy = ["test"]
`)
	if _, err := Load(dir); err == nil {
		t.Fatal("expected strict-parse error for unknown stacks field")
	}
}

// TestLoadSubprojectsRejectsOnlyAndSkip — `only` and `skip` on the same
// subproject are mutually exclusive and fail at load.
func TestLoadSubprojectsRejectsOnlyAndSkip(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "stratt.toml", `
[[subprojects]]
path = "frontend"
only = ["build"]
skip = ["test"]
`)
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error when both only and skip are set")
	}
}
