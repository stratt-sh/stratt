package detect

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// touch creates an empty file inside root, including any parent directories.
func touch(t *testing.T, root, rel string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestDetectGo(t *testing.T) {
	dir := t.TempDir()
	if got := detectGo(dir); got.Name != "" {
		t.Fatalf("empty repo should not match: got %+v", got)
	}
	touch(t, dir, "go.mod")
	if got := detectGo(dir); got.Name != "go" || got.Signal != "go.mod" {
		t.Fatalf("go.mod present: got %+v", got)
	}
}

func TestDetectNodeNPM(t *testing.T) {
	dir := t.TempDir()
	if got := detectNodeNPM(dir); got.Name != "" {
		t.Fatalf("empty repo should not match: got %+v", got)
	}
	touch(t, dir, "package.json")
	if got := detectNodeNPM(dir); got.Name != "" {
		t.Fatalf("package.json without package-lock.json should not match: got %+v", got)
	}
	touch(t, dir, "package-lock.json")
	if got := detectNodeNPM(dir); got.Name != "node+npm" || got.Signal != "package.json + package-lock.json" {
		t.Fatalf("package.json + package-lock.json: got %+v", got)
	}
}

func TestDetectPythonUV(t *testing.T) {
	dir := t.TempDir()

	touch(t, dir, "pyproject.toml")
	if got := detectPythonUV(dir); got.Name != "" {
		t.Fatalf("pyproject without uv.lock should not match: got %+v", got)
	}

	touch(t, dir, "uv.lock")
	if got := detectPythonUV(dir); got.Name != "python+uv" {
		t.Fatalf("pyproject + uv.lock: got %+v", got)
	}
}

func TestDetectPHP(t *testing.T) {
	dir := t.TempDir()
	if got := detectPHP(dir); got.Name != "" {
		t.Fatalf("empty repo: got %+v", got)
	}
	touch(t, dir, "composer.json")
	if got := detectPHP(dir); got.Name != "php" {
		t.Fatalf("composer.json present: got %+v", got)
	}
}

func TestDetectDocker(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "Dockerfile")
	if got := detectDocker(dir); got.Name != "docker" {
		t.Fatalf("Dockerfile present: got %+v", got)
	}
}

func TestDetectKustomize(t *testing.T) {
	dir := t.TempDir()

	// Just the overlays directory with no kustomization.yaml does not match.
	if err := os.MkdirAll(filepath.Join(dir, "deploy", "overlays", "prod"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := detectKustomize(dir); got.Name != "" {
		t.Fatalf("empty overlay should not match: got %+v", got)
	}

	touch(t, dir, "deploy/overlays/prod/kustomization.yaml")
	if got := detectKustomize(dir); got.Name != "kustomize" {
		t.Fatalf("overlay with kustomization.yaml: got %+v", got)
	}
}

func TestDetectMkDocs(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "mkdocs.yml")
	if got := detectMkDocs(dir); got.Name != "mkdocs" {
		t.Fatalf("mkdocs.yml present: got %+v", got)
	}
}

func TestDetectSphinx(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "docs/conf.py")
	if got := detectSphinx(dir); got.Name != "sphinx" {
		t.Fatalf("docs/conf.py present: got %+v", got)
	}
}

// TestDetectHugoAtRoot — Hugo config at the repo root (typical for a
// dedicated docs/site repo).
func TestDetectHugoAtRoot(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "hugo.toml")
	got := detectHugo(dir)
	if got.Name != "hugo" {
		t.Errorf("got %+v", got)
	}
	if got.Signal != "hugo.toml" {
		t.Errorf("signal: got %q", got.Signal)
	}
	if src := FindHugoSource(dir); src != "." {
		t.Errorf("FindHugoSource: got %q, want \".\"", src)
	}
}

// TestDetectHugoInDocsSubdir — Hugo config in docs/ (stratt's own
// layout — code lives at the root, docs site nested).
func TestDetectHugoInDocsSubdir(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "docs/hugo.toml")
	got := detectHugo(dir)
	if got.Name != "hugo" {
		t.Errorf("got %+v", got)
	}
	if got.Signal != "docs/hugo.toml" {
		t.Errorf("signal: got %q", got.Signal)
	}
	if src := FindHugoSource(dir); src != "docs" {
		t.Errorf("FindHugoSource: got %q, want \"docs\"", src)
	}
}

// TestDetectHugoMultipleFormats — Hugo accepts toml/yaml/yml/json
// config files; we detect any of them.
func TestDetectHugoMultipleFormats(t *testing.T) {
	for _, name := range []string{"hugo.yaml", "hugo.yml", "hugo.json"} {
		dir := t.TempDir()
		touch(t, dir, name)
		if got := detectHugo(dir); got.Name != "hugo" {
			t.Errorf("%s: not detected", name)
		}
	}
}

// TestDetectHugoRootBeatsDocs — if both a root- and docs-level config
// exist, the root one wins (matches Hugo's own discovery order).
func TestDetectHugoRootBeatsDocs(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "hugo.toml")
	touch(t, dir, "docs/hugo.toml")
	if src := FindHugoSource(dir); src != "." {
		t.Errorf("FindHugoSource: got %q, want root", src)
	}
}

// TestDetectHugoAbsent — no Hugo config means no match.
func TestDetectHugoAbsent(t *testing.T) {
	if got := detectHugo(t.TempDir()); got.Name != "" {
		t.Errorf("empty repo: got %+v", got)
	}
	if src := FindHugoSource(t.TempDir()); src != "" {
		t.Errorf("empty repo: src = %q, want empty", src)
	}
}

// writeFile writes body to root/rel, creating parent dirs as needed.
func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %v", err)
	}
}

const galaxyYML = `namespace: zebpalmer
name: tailscale
version: 0.7.2
readme: README.md
`

func TestDetectAnsibleCollection(t *testing.T) {
	dir := t.TempDir()
	if got := detectAnsibleCollection(dir); got.Name != "" {
		t.Fatalf("empty repo: got %+v", got)
	}
	writeFile(t, dir, "galaxy.yml", galaxyYML)
	if got := detectAnsibleCollection(dir); got.Name != "ansible-collection" {
		t.Fatalf("galaxy.yml present: got %+v", got)
	}
}

// TestDetectAnsibleCollectionRequiresBothKeys — a galaxy.yml missing
// either `namespace:` or `name:` is not a collection (it might be a
// role meta file with a different schema).
func TestDetectAnsibleCollectionRequiresBothKeys(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "galaxy.yml", "name: tailscale\nversion: 1.0.0\n")
	if got := detectAnsibleCollection(dir); got.Name != "" {
		t.Fatalf("name-only galaxy.yml should not match: got %+v", got)
	}
}

func TestDetectAnsibleRole(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "roles/machine/tasks/main.yml")
	touch(t, dir, "roles/machine/meta/main.yml")
	if got := detectAnsibleRole(dir); got.Name != "ansible-role" {
		t.Fatalf("role layout present: got %+v", got)
	}
}

func TestDetectAnsiblePlaybook(t *testing.T) {
	dir := t.TempDir()
	if got := detectAnsiblePlaybook(dir); got.Name != "" {
		t.Fatalf("empty repo: got %+v", got)
	}
	touch(t, dir, "ansible.cfg")
	if got := detectAnsiblePlaybook(dir); got.Name != "ansible-playbook" {
		t.Fatalf("ansible.cfg present: got %+v", got)
	}
}

// TestDetectAnsiblePlaybookSkippedInsideCollection — a collection that
// happens to ship ansible.cfg for local testing should still detect as
// a collection only.
func TestDetectAnsiblePlaybookSkippedInsideCollection(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "galaxy.yml", galaxyYML)
	touch(t, dir, "ansible.cfg")
	if got := detectAnsiblePlaybook(dir); got.Name != "" {
		t.Fatalf("playbook detector should defer to collection: got %+v", got)
	}
}

// TestDetectAnsibleRoleSkippedInsideCollection — when galaxy.yml is
// present the role inside the collection should not be reported as a
// standalone role stack.
func TestDetectAnsibleRoleSkippedInsideCollection(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "galaxy.yml", galaxyYML)
	touch(t, dir, "roles/machine/tasks/main.yml")
	touch(t, dir, "roles/machine/meta/main.yml")
	if got := detectAnsibleRole(dir); got.Name != "" {
		t.Fatalf("role inside collection should not match: got %+v", got)
	}
}

// TestScanMultiStack covers a representative multi-stack repo:
// python+uv + docker + kustomize + mkdocs.  Names should come back
// sorted alphabetically per Scan's contract.
func TestScanMultiStack(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "pyproject.toml")
	touch(t, dir, "uv.lock")
	touch(t, dir, "Dockerfile")
	touch(t, dir, "deploy/overlays/prod/kustomization.yaml")
	touch(t, dir, "mkdocs.yml")

	got := Scan(dir)
	if got.Root != dir {
		t.Errorf("Root: got %q, want %q", got.Root, dir)
	}

	names := make([]string, len(got.Stacks))
	for i, s := range got.Stacks {
		names[i] = s.Name
	}
	want := []string{"docker", "kustomize", "mkdocs", "python+uv"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("stack names: got %v, want %v", names, want)
	}

	// Confirm sorted ordering is stable.
	if !sort.StringsAreSorted(names) {
		t.Errorf("stacks should be returned in sorted order, got %v", names)
	}
}

func TestScanEmpty(t *testing.T) {
	dir := t.TempDir()
	got := Scan(dir)
	if len(got.Stacks) != 0 {
		t.Errorf("empty repo: expected 0 stacks, got %v", got.Stacks)
	}
}

// TestScanRepoMonorepo — the emulsia shape: no language stack at the
// root (just a Dockerfile + kustomize), Python in backend/, Node in
// frontend/.  Subprojects come back sorted by Dir, each with its own stacks.
func TestScanRepoMonorepo(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "Dockerfile")
	touch(t, dir, "deploy/overlays/prod/kustomization.yaml")
	touch(t, dir, "backend/pyproject.toml")
	touch(t, dir, "backend/uv.lock")
	touch(t, dir, "frontend/package.json")
	touch(t, dir, "frontend/package-lock.json")

	ws := ScanRepo(dir)

	// Root still reports its auxiliary stacks.
	rootNames := stackNames(ws.Root.Stacks)
	if !reflect.DeepEqual(rootNames, []string{"docker", "kustomize"}) {
		t.Errorf("root stacks: got %v", rootNames)
	}

	if len(ws.Subprojects) != 2 {
		t.Fatalf("expected 2 members, got %d: %+v", len(ws.Subprojects), ws.Subprojects)
	}
	if ws.Subprojects[0].Dir != "backend" || ws.Subprojects[1].Dir != "frontend" {
		t.Errorf("member order/dirs: got %q, %q", ws.Subprojects[0].Dir, ws.Subprojects[1].Dir)
	}
	if got := stackNames(ws.Subprojects[0].Report.Stacks); !reflect.DeepEqual(got, []string{"python+uv"}) {
		t.Errorf("backend stacks: got %v", got)
	}
	if got := stackNames(ws.Subprojects[1].Report.Stacks); !reflect.DeepEqual(got, []string{"node+npm"}) {
		t.Errorf("frontend stacks: got %v", got)
	}
}

// TestScanRepoIgnoresJunkDirs — node_modules, .venv, dot-dirs, and
// the root-conventional deploy/ and docs/ are never reported as members,
// even when they contain manifest-shaped files.
func TestScanRepoIgnoresJunkDirs(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "node_modules/foo/package.json")
	touch(t, dir, "node_modules/foo/package-lock.json")
	touch(t, dir, ".venv/pyproject.toml")
	touch(t, dir, ".venv/uv.lock")
	touch(t, dir, "deploy/go.mod")
	touch(t, dir, "docs/package.json")
	touch(t, dir, "docs/package-lock.json")

	ws := ScanRepo(dir)
	if len(ws.Subprojects) != 0 {
		t.Errorf("expected no members from junk dirs, got %+v", ws.Subprojects)
	}
}

// TestScanRepoSingleStackRootHasNoMembers — a conventional repo with
// a manifest at the root produces no members (ScanRepo still scans,
// but the policy gate lives in capability; here we just confirm an
// incidental subdir without a stack isn't picked up).
func TestScanRepoNoStackSubdirsSkipped(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "go.mod")
	touch(t, dir, "internal/foo.go") // a plain source subdir, no manifest
	ws := ScanRepo(dir)
	if len(ws.Subprojects) != 0 {
		t.Errorf("source-only subdir should not be a member, got %+v", ws.Subprojects)
	}
}

func stackNames(stacks []Stack) []string {
	out := make([]string, len(stacks))
	for i, s := range stacks {
		out[i] = s.Name
	}
	return out
}
