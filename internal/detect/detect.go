// Package detect identifies the project stacks present in a repository.
//
// Each Detector reports a single Stack (e.g., "go", "python+uv", "kustomize")
// based on the presence of well-known signal files.  A single repository can
// have multiple stacks; mixed Python + Docker + Kustomize is normal.
//
// See requirements R2.1 for the full detection signal table.
package detect

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Stack is one detected project stack.
type Stack struct {
	Name   string // human-readable name, e.g., "python+uv"
	Signal string // the file or pattern that triggered detection
}

// Report is the result of scanning a directory.
type Report struct {
	Root   string
	Stacks []Stack
}

// detector is the predicate-shape used internally.  Each returns a non-empty
// Stack when its signal is present in root, or the zero value otherwise.
type detector func(root string) Stack

// detectors is the ordered list of stack detectors.  Order is reported order
// only — it has no effect on which detectors run.
var detectors = []detector{
	detectGo,
	detectPythonUV,
	detectPHP,
	detectDocker,
	detectKustomize,
	detectMkDocs,
	detectSphinx,
	detectHugo,
	detectGitHubActions,
	detectAnsibleCollection,
	detectAnsibleRole,
}

// Scan runs all detectors against root and returns the report.
func Scan(root string) Report {
	r := Report{Root: root}
	for _, d := range detectors {
		if s := d(root); s.Name != "" {
			r.Stacks = append(r.Stacks, s)
		}
	}
	sort.Slice(r.Stacks, func(i, j int) bool {
		return r.Stacks[i].Name < r.Stacks[j].Name
	})
	return r
}

func detectGo(root string) Stack {
	if exists(filepath.Join(root, "go.mod")) {
		return Stack{Name: "go", Signal: "go.mod"}
	}
	return Stack{}
}

func detectPythonUV(root string) Stack {
	if exists(filepath.Join(root, "pyproject.toml")) && exists(filepath.Join(root, "uv.lock")) {
		return Stack{Name: "python+uv", Signal: "pyproject.toml + uv.lock"}
	}
	return Stack{}
}

func detectPHP(root string) Stack {
	if exists(filepath.Join(root, "composer.json")) {
		return Stack{Name: "php", Signal: "composer.json"}
	}
	return Stack{}
}

func detectDocker(root string) Stack {
	if exists(filepath.Join(root, "Dockerfile")) {
		return Stack{Name: "docker", Signal: "Dockerfile"}
	}
	return Stack{}
}

func detectKustomize(root string) Stack {
	overlays := filepath.Join(root, "deploy", "overlays")
	entries, err := os.ReadDir(overlays)
	if err != nil {
		return Stack{}
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if exists(filepath.Join(overlays, e.Name(), "kustomization.yaml")) {
			return Stack{Name: "kustomize", Signal: "deploy/overlays/*/kustomization.yaml"}
		}
	}
	return Stack{}
}

func detectMkDocs(root string) Stack {
	if exists(filepath.Join(root, "mkdocs.yml")) {
		return Stack{Name: "mkdocs", Signal: "mkdocs.yml"}
	}
	return Stack{}
}

func detectSphinx(root string) Stack {
	if exists(filepath.Join(root, "docs", "conf.py")) {
		return Stack{Name: "sphinx", Signal: "docs/conf.py"}
	}
	return Stack{}
}

// detectHugo checks for a Hugo site config file either at the repo
// root (typical for dedicated docs repos) or inside a `docs/`
// subdirectory (typical for projects that ship code AND docs in the
// same repo — stratt's own layout).  Detection succeeds for any of
// hugo.{toml,yaml,yml,json}; FindHugoSource returns which directory
// to point Hugo at.
func detectHugo(root string) Stack {
	if src, name := findHugoConfigIn(root); src != "" {
		signal := name
		if src != "." {
			signal = filepath.Join(src, name)
		}
		return Stack{Name: "hugo", Signal: signal}
	}
	return Stack{}
}

// FindHugoSource returns the Hugo project directory (relative to root)
// or "" if no Hugo config is present.  Used by the docs subcommand to
// invoke `hugo --source <dir>`.
func FindHugoSource(root string) string {
	src, _ := findHugoConfigIn(root)
	return src
}

// findHugoConfigIn returns (directory, basename) of the first Hugo
// config file found, searching the repo root then docs/.  Empty
// directory string means "not found".
func findHugoConfigIn(root string) (dir, name string) {
	for _, sub := range []string{".", "docs"} {
		for _, n := range []string{"hugo.toml", "hugo.yaml", "hugo.yml", "hugo.json"} {
			if exists(filepath.Join(root, sub, n)) {
				return sub, n
			}
		}
	}
	return "", ""
}

// detectGitHubActions matches a GitHub Actions repo in either shape:
//
//   - composite / reusable action: `action.yml` or `action.yaml` at the
//     repo root (e.g., setup-stratt)
//   - workflows-only repo: at least one .yml/.yaml file in
//     .github/workflows/ (e.g., a .github org repo)
//
// The stack name is the same in both cases — the chain only cares that
// stratt is looking at actions YAML.
func detectGitHubActions(root string) Stack {
	for _, n := range []string{"action.yml", "action.yaml"} {
		if exists(filepath.Join(root, n)) {
			return Stack{Name: "github-actions", Signal: n}
		}
	}
	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Stack{}
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml") {
			return Stack{Name: "github-actions", Signal: ".github/workflows/*.yml"}
		}
	}
	return Stack{}
}

// detectAnsibleCollection matches an Ansible collection repo by the
// presence of galaxy.yml at the root with both `namespace:` and `name:`
// keys.  The two-key requirement avoids a false positive on bare role
// repositories that ship a Galaxy-style meta file without being a
// collection.
func detectAnsibleCollection(root string) Stack {
	path := filepath.Join(root, "galaxy.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		return Stack{}
	}
	if hasYAMLKey(data, "namespace") && hasYAMLKey(data, "name") {
		return Stack{Name: "ansible-collection", Signal: "galaxy.yml"}
	}
	return Stack{}
}

// detectAnsibleRole matches a standalone Ansible role: at least one
// roles/*/tasks/main.{yml,yaml} alongside roles/*/meta/main.{yml,yaml}
// in the same role directory, with no galaxy.yml at the root.  Skipped
// when ansible-collection has already matched — roles inside a
// collection are not independently versioned.
func detectAnsibleRole(root string) Stack {
	if exists(filepath.Join(root, "galaxy.yml")) {
		return Stack{}
	}
	rolesDir := filepath.Join(root, "roles")
	entries, err := os.ReadDir(rolesDir)
	if err != nil {
		return Stack{}
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		role := filepath.Join(rolesDir, e.Name())
		if hasAnyFile(role, "tasks/main.yml", "tasks/main.yaml") &&
			hasAnyFile(role, "meta/main.yml", "meta/main.yaml") {
			return Stack{Name: "ansible-role", Signal: "roles/*/tasks/main.yml"}
		}
	}
	return Stack{}
}

func hasAnyFile(root string, rels ...string) bool {
	for _, r := range rels {
		if exists(filepath.Join(root, r)) {
			return true
		}
	}
	return false
}

// hasYAMLKey reports whether data contains a top-level YAML key matching
// name (i.e. `<name>:` at column 0).  Sufficient for the detection
// signals we care about (galaxy.yml is a simple flat document) without
// dragging in a YAML parser.
func hasYAMLKey(data []byte, name string) bool {
	prefix := name + ":"
	for _, line := range strings.Split(string(data), "\n") {
		// Strip a trailing CR for CRLF-formatted files.
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
