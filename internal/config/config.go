// Package config loads and validates stratt's TOML project configuration.
//
// Per R2.3, project config lives in one of two places:
//   - `stratt.toml` at repo root (top-level tables)
//   - `[tool.stratt]` section in `pyproject.toml` (nested tables)
//
// Having both is a fatal error (R2.3.3).  The schemas are identical;
// only the surrounding key path differs.
//
// Parsing is strict: unknown fields fail at load time (R2.3.8), which is
// stratt's compatibility safety net in lieu of a `schema_version` field.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

// Project is the user-facing project configuration.  A zero-value
// Project is valid and represents a zero-config repository — detection
// drives all behavior.
type Project struct {
	// Source is the file this config was loaded from, useful for error
	// messages.  Empty if no config file existed.
	Source string

	// RequiredStratt is the semver constraint (e.g. ">= 1.2") from
	// `required_stratt` under [tool.stratt] or top-level.  Empty if unset.
	RequiredStratt string

	// Tasks are public (visible to `stratt help`) named tasks.
	Tasks map[string]Task

	// Helpers are hidden tasks; same shape as Tasks but omitted from
	// `stratt help` output.  See R2.6.10.
	Helpers map[string]Task

	// Bump is the optional native bump-engine configuration (R2.4.7
	// item 1).  Nil if the project uses legacy [tool.bumpversion] or
	// no bump config at all.
	Bump *Bump

	// Release captures the optional [release] section.  Nil when no
	// [release] table is present; release flow falls back to its own
	// defaults (branch auto-detection, push enabled).
	Release *Release

	// Deploy captures the optional [deploy] section.  Nil when absent;
	// deploy flow falls back to its own defaults.
	Deploy *Deploy

	// Subprojects are explicitly-declared subprojects ([[subprojects]]).
	// Each names a subdirectory whose stack the per-project verbs (build,
	// test, lint, ...) fan out into.  Empty means "auto-detect" — stratt
	// scans immediate subdirectories when the repo root has no language
	// stack of its own.  Declaring subprojects opts in even when the root
	// has a stack, and pins the subproject set and order.
	Subprojects []Subproject
}

// Subproject is one declared subproject ([[subprojects]]).
type Subproject struct {
	// Path is the subproject directory relative to the repo root (e.g.
	// "backend").  "." includes the repo root itself as a subproject.
	Path string

	// Only, when non-empty, restricts which universal verbs fan out to
	// this subproject (e.g. ["test", "lint"]).  Empty means all per-stack
	// verbs fan out here.  Mutually exclusive with Skip.
	Only []string

	// Skip lists universal verbs that should NOT fan out to this
	// subproject (e.g. ["test", "format"] for a frontend with no test or
	// format phase).  Mutually exclusive with Only.
	Skip []string
}

// Release is the schema for [release] / [tool.stratt.release] (R2.3.6).
// All fields are pointers/empty-checks so the absence of a setting
// triggers stratt's defaults rather than zero-value coercion.
type Release struct {
	// Branch is the release branch.  Empty means "auto-detect"
	// (prefer main, fall back to master).
	Branch string

	// Push controls whether to push commit/tag to remote.  Pointer so
	// nil means "use stratt's default" (which is true per R2.4.5).
	Push *bool

	// Commit controls whether to create the bump commit.  Pointer so
	// nil means default (true).  `commit = false` is the "review then
	// merge" workflow.
	Commit *bool

	// Remote is the git remote to push to.  Empty means "origin".
	Remote string

	// SyncLockfiles controls whether the release flow re-syncs ecosystem
	// lockfiles (uv.lock, package-lock.json) after the version bump so
	// they land inside the release commit.  Pointer so nil means "use
	// stratt's default" (true).
	SyncLockfiles *bool
}

// Deploy is the schema for [deploy] / [tool.stratt.deploy].  Mirrors
// Release's commit/push override pattern and adds a primary_image
// shortcut for multi-image overlays.
type Deploy struct {
	// PrimaryImage is the image name to bump when the overlay has more
	// than one and the user didn't pass --image.  Empty means "the only
	// image" if there's exactly one.
	PrimaryImage string

	// Push / Commit have the same semantics as Release.  Defaults true.
	Push   *bool
	Commit *bool
}

// Task captures the schema for [tasks.NAME] and [helpers.NAME] (R2.6).
// Fields are lowered to their canonical forms by Load: Run is always
// a (possibly empty) list, Enabled is always set (default true).
type Task struct {
	// Description is shown in `stratt help`.
	Description string

	// Tasks lists other task names to execute first, in order.
	Tasks []string

	// Before runs shell commands before the body.  Only valid in
	// augment mode (Run empty against a built-in name).
	Before []string

	// Run is the task body — one or more shell commands.  When non-empty
	// against a built-in name, it overrides the built-in entirely.
	Run []string

	// After runs shell commands after the body.  Only valid in augment
	// mode.
	After []string

	// Enabled lets users disable a task (R2.6.2).  Always populated;
	// default is true.
	Enabled bool
}

// Bump is the schema for [bump] / [tool.stratt.bump] (R2.4.10).
// Only the v1 feature set is represented here.
type Bump struct {
	CurrentVersion  string
	Files           []string
	MessageTemplate string // git commit message; default applied at run time
	TagPrefix       string // default "v"
}

// raw types mirror the on-disk TOML shape.  They are intentionally
// distinct from the public types so we can run validation/normalization
// in Load before exposing the final Project.

type rawProject struct {
	RequiredStratt string             `toml:"required_stratt"`
	Tasks          map[string]rawTask `toml:"tasks"`
	Helpers        map[string]rawTask `toml:"helpers"`
	Bump           *rawBump           `toml:"bump"`
	Release        *rawRelease        `toml:"release"`
	Deploy         *rawDeploy         `toml:"deploy"`
	Subprojects    []rawSubproject    `toml:"subprojects"`
}

type rawSubproject struct {
	Path string   `toml:"path"`
	Only []string `toml:"only"`
	Skip []string `toml:"skip"`
}

type rawRelease struct {
	Branch        string `toml:"branch"`
	Push          *bool  `toml:"push"`
	Commit        *bool  `toml:"commit"`
	Remote        string `toml:"remote"`
	SyncLockfiles *bool  `toml:"sync_lockfiles"`
}

type rawDeploy struct {
	PrimaryImage string `toml:"primary_image"`
	Push         *bool  `toml:"push"`
	Commit       *bool  `toml:"commit"`
}

type rawTask struct {
	Description string   `toml:"description"`
	Tasks       []string `toml:"tasks"`
	Before      []string `toml:"before"`
	Run         any      `toml:"run"` // string | []string
	After       []string `toml:"after"`
	Enabled     *bool    `toml:"enabled"`
}

// rawBump captures the [bump] / [tool.stratt.bump] section.  Files is
// `any` because the value can be a list of strings (stratt-native shape)
// or a list of tables ([[files]] with filename/search/replace; the
// bump-my-version shape that stratt also accepts during the v1
// transition).  The bump package's loader does the full structural
// parsing; the config package only needs to recognize presence.
//
// All fields the bump package accepts must be listed here too, even
// when the config package doesn't surface them — `loadStrict` enforces
// DisallowUnknownFields document-wide (R2.3.8), so any field present in
// real bump configs but absent from this struct fails the strict gate.
type rawBump struct {
	CurrentVersion  string `toml:"current_version"`
	Files           any    `toml:"files"`
	MessageTemplate string `toml:"message_template"`
	TagPrefix       string `toml:"tag_prefix"`

	// Mirror of the fields recognized by bump/loader.go's
	// rawBumpVersion.  Parsed here only to keep strict mode happy;
	// the bump package re-reads the file for its own decoding.
	Search               string   `toml:"search"`
	Replace              string   `toml:"replace"`
	Tag                  *bool    `toml:"tag"`
	TagName              string   `toml:"tag_name"`
	Commit               *bool    `toml:"commit"`
	Message              string   `toml:"message"`
	Parse                string   `toml:"parse"`
	Serialize            []string `toml:"serialize"`
	Regex                *bool    `toml:"regex"`
	IgnoreMissingVersion *bool    `toml:"ignore_missing_version"`
	SignTags             *bool    `toml:"sign_tags"`
	TagMessage           string   `toml:"tag_message"`
	AllowDirty           *bool    `toml:"allow_dirty"`
	CommitArgs           string   `toml:"commit_args"`
	PreCommitHooks       []string `toml:"pre_commit_hooks"`
}

// ErrConflict is returned when both stratt.toml exists AND pyproject.toml
// contains a [tool.stratt] table (R2.3.3).  Callers should surface this
// to the user verbatim — the message is intentionally explicit.
var ErrConflict = errors.New(
	"stratt config conflict: both stratt.toml and [tool.stratt] in pyproject.toml exist; " +
		"consolidate into one",
)

// Load reads and validates project config in root.  Returns a zero-value
// Project (no error) when no config file exists, which is the common
// case — stratt is fully functional with no config.
func Load(root string) (*Project, error) {
	strattPath := filepath.Join(root, "stratt.toml")
	pyprojectPath := filepath.Join(root, "pyproject.toml")

	hasStrattToml := fileExists(strattPath)
	pyStratt, err := extractPyprojectStratt(pyprojectPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", pyprojectPath, err)
	}
	hasPyprojectStratt := pyStratt != nil

	if hasStrattToml && hasPyprojectStratt {
		return nil, ErrConflict
	}

	var raw *rawProject
	var source string
	switch {
	case hasStrattToml:
		raw = &rawProject{}
		if err := loadStrict(strattPath, raw); err != nil {
			return nil, fmt.Errorf("reading %s: %w", strattPath, err)
		}
		source = strattPath
	case hasPyprojectStratt:
		raw = pyStratt
		source = pyprojectPath
	default:
		// No config at all; entirely valid (zero-config repo).
		return &Project{}, nil
	}

	proj, err := normalize(raw)
	if err != nil {
		return nil, fmt.Errorf("validating %s: %w", source, err)
	}
	proj.Source = source
	return proj, nil
}

// loadStrict reads path into target with DisallowUnknownFields enabled.
// This implements R2.3.8 strict-unknown-field parsing.
func loadStrict(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return enrichTOMLError(dec.Decode(target))
}

// extractPyprojectStratt locates the [tool.stratt] subtree in pyproject.toml
// (if any) and decodes it strictly.  Other [tool.X] tables remain
// untouched — strictness applies only within the stratt namespace per
// the spirit of R2.3.8 (we can't reject unknown fields under [tool.uv]
// without breaking every Python repo).
//
// Returns (nil, nil) when pyproject.toml is missing or has no
// [tool.stratt] table.
func extractPyprojectStratt(path string) (*rawProject, error) {
	if !fileExists(path) {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Permissive top-level parse to find the subtree.
	var top map[string]any
	if err := toml.Unmarshal(data, &top); err != nil {
		return nil, err
	}
	toolMap, ok := top["tool"].(map[string]any)
	if !ok {
		return nil, nil
	}
	strattMap, ok := toolMap["stratt"].(map[string]any)
	if !ok {
		return nil, nil
	}

	// Re-marshal just the stratt subtree and decode it strictly so
	// unknown fields within stratt's namespace fail loudly.
	subBytes, err := toml.Marshal(strattMap)
	if err != nil {
		return nil, err
	}
	var raw rawProject
	dec := toml.NewDecoder(bytes.NewReader(subBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("[tool.stratt]: %w", enrichTOMLError(err))
	}
	return &raw, nil
}

// normalize converts raw decoder output into the public Project type:
//   - run: string | []string → []string
//   - enabled: *bool → bool (default true)
//   - task / helper name conflict detection (R2.6.10)
//   - sanity checks on shape (R2.6.1)
func normalize(raw *rawProject) (*Project, error) {
	p := &Project{
		RequiredStratt: raw.RequiredStratt,
		Tasks:          map[string]Task{},
		Helpers:        map[string]Task{},
	}

	for name, rt := range raw.Tasks {
		t, err := normalizeTask(name, rt, false)
		if err != nil {
			return nil, err
		}
		p.Tasks[name] = t
	}
	for name, rt := range raw.Helpers {
		t, err := normalizeTask(name, rt, true)
		if err != nil {
			return nil, err
		}
		// Duplicate-across-sections check.
		if _, dup := p.Tasks[name]; dup {
			return nil, fmt.Errorf(
				"task %q defined in both [tasks] and [helpers]; pick one", name)
		}
		p.Helpers[name] = t
	}

	if raw.Release != nil {
		p.Release = &Release{
			Branch:        raw.Release.Branch,
			Push:          raw.Release.Push,
			Commit:        raw.Release.Commit,
			Remote:        raw.Release.Remote,
			SyncLockfiles: raw.Release.SyncLockfiles,
		}
	}

	if raw.Deploy != nil {
		p.Deploy = &Deploy{
			PrimaryImage: raw.Deploy.PrimaryImage,
			Push:         raw.Deploy.Push,
			Commit:       raw.Deploy.Commit,
		}
	}

	for i, rs := range raw.Subprojects {
		if rs.Path == "" {
			return nil, fmt.Errorf("subprojects[%d]: `path` is required", i)
		}
		if len(rs.Only) > 0 && len(rs.Skip) > 0 {
			return nil, fmt.Errorf(
				"subprojects[%d] (%q): set `only` or `skip`, not both", i, rs.Path)
		}
		p.Subprojects = append(p.Subprojects, Subproject{
			Path: rs.Path,
			Only: append([]string(nil), rs.Only...),
			Skip: append([]string(nil), rs.Skip...),
		})
	}

	if raw.Bump != nil {
		p.Bump = &Bump{
			CurrentVersion:  raw.Bump.CurrentVersion,
			MessageTemplate: raw.Bump.MessageTemplate,
			TagPrefix:       raw.Bump.TagPrefix,
		}
		// Files is opaque here — bump engine reads it fully via the bump package.
		if files, ok := raw.Bump.Files.([]any); ok {
			for _, f := range files {
				if s, ok := f.(string); ok {
					p.Bump.Files = append(p.Bump.Files, s)
				}
			}
		}
	}

	return p, nil
}

// normalizeTask handles the run-field union (string | []string) and
// applies the enabled default.
func normalizeTask(name string, rt rawTask, isHelper bool) (Task, error) {
	t := Task{
		Description: rt.Description,
		Tasks:       append([]string(nil), rt.Tasks...),
		Before:      append([]string(nil), rt.Before...),
		After:       append([]string(nil), rt.After...),
		Enabled:     true,
	}
	if rt.Enabled != nil {
		t.Enabled = *rt.Enabled
	}

	switch v := rt.Run.(type) {
	case nil:
		// No run — augment mode (against a built-in) or no-op user task.
	case string:
		t.Run = []string{v}
	case []any:
		for i, e := range v {
			s, ok := e.(string)
			if !ok {
				return Task{}, fmt.Errorf(
					"%s.%s.run[%d] must be a string, got %T", sectionFor(isHelper), name, i, e)
			}
			t.Run = append(t.Run, s)
		}
	case []string:
		t.Run = append(t.Run, v...)
	default:
		return Task{}, fmt.Errorf(
			"%s.%s.run must be a string or list of strings, got %T", sectionFor(isHelper), name, v)
	}
	return t, nil
}

func sectionFor(isHelper bool) string {
	if isHelper {
		return "helpers"
	}
	return "tasks"
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
