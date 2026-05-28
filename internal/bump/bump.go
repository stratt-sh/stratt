// Package bump is stratt's native version-bump engine.
//
// Per R2.4.6, stratt does NOT shell out to bump-my-version.  This
// package implements the bump → commit → tag → push flow natively,
// reading the same on-disk config formats existing repos already use
// (R2.4.7) so adoption requires zero migration:
//
//   - native:   [bump] in stratt.toml  OR  [tool.stratt.bump] in pyproject.toml
//   - compat:   [tool.bumpversion] in pyproject.toml
//   - compat:   .bumpversion.toml
//   - compat:   .bumpversion.cfg  (legacy INI — recognized but emits a deprecation note)
//
// v1 feature set (R2.4.10): semver patch/minor/major bumps, per-file
// search/replace with {current_version}/{new_version} template
// substitution, configurable commit message and tag prefix, optional
// commit+tag+push.  pre/post hooks, custom serialize/parse formats,
// and sign_tags are deferred.
package bump

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Kind is the bump granularity.
type Kind int

const (
	Patch Kind = iota
	Minor
	Major
)

func (k Kind) String() string {
	switch k {
	case Patch:
		return "patch"
	case Minor:
		return "minor"
	case Major:
		return "major"
	}
	return "?"
}

// KindFromString parses one of "patch", "minor", "major" (case-insensitive).
func KindFromString(s string) (Kind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "patch":
		return Patch, nil
	case "minor":
		return Minor, nil
	case "major":
		return Major, nil
	}
	return 0, fmt.Errorf("invalid bump kind %q (want patch|minor|major)", s)
}

// Config is the loaded bump configuration, normalized across the four
// supported on-disk formats.
type Config struct {
	// Source is the file the config was read from, for error messages.
	Source string

	// CurrentVersion is the version string before the bump.
	CurrentVersion string

	// SearchTemplate / ReplaceTemplate are the top-level defaults for
	// per-file find-and-replace.  Each FileEntry can override.  Templates
	// can contain {current_version} and {new_version}.
	SearchTemplate  string
	ReplaceTemplate string

	// Files is the list of per-file edits.
	Files []FileEntry

	// Commit, Tag — control whether to create a git commit / tag.
	Commit bool
	Tag    bool

	// MessageTemplate is the commit message.  Templates {current_version}
	// and {new_version} substitute.
	MessageTemplate string

	// TagNameTemplate is the tag name.  Same template variables.
	TagNameTemplate string
}

// FileEntry describes one file to edit during a bump.
type FileEntry struct {
	Filename string
	Search   string // empty → inherit from Config.SearchTemplate
	Replace  string // empty → inherit from Config.ReplaceTemplate
	// ReplaceAll, when true, replaces every occurrence of Search in the
	// file.  Default (false) replaces only the first match — matching
	// bump-my-version's behavior.  Useful for files like READMEs that
	// reference the version in multiple places.
	ReplaceAll bool
}

// Plan is the result of computing a bump.  All fields are populated
// without touching the filesystem; Apply commits the changes.
type Plan struct {
	Cfg           *Config
	OldVersion    string
	NewVersion    string
	FileChanges   []FileChange
	CommitMessage string
	TagName       string
}

// FileChange describes a single edit Apply will make.
type FileChange struct {
	Path     string
	OldChunk string // search string with templates substituted
	NewChunk string // replace string with templates substituted
	// Found reports whether the search string was found in the file.
	// A FileChange with Found == false will fail Apply unless the
	// caller filters it out, matching bump-my-version's
	// `ignore_missing_version = false` default.
	Found bool
	// ReplaceAll mirrors FileEntry.ReplaceAll — when true, every
	// occurrence of OldChunk in the file is rewritten, not just the
	// first.
	ReplaceAll bool
}

// Compute returns a Plan for bumping cfg by kind.  The plan is
// deterministic — calling Compute multiple times yields identical output
// — and side-effect-free, so callers can show a preview before applying.
func Compute(cfg *Config, kind Kind, root string) (*Plan, error) {
	if cfg.CurrentVersion == "" {
		return nil, errors.New("bump config has no current_version")
	}
	next, err := bumpSemver(cfg.CurrentVersion, kind)
	if err != nil {
		return nil, err
	}
	plan := &Plan{
		Cfg:        cfg,
		OldVersion: cfg.CurrentVersion,
		NewVersion: next,
	}
	for _, fe := range cfg.Files {
		search := orDefault(fe.Search, cfg.SearchTemplate, "{current_version}")
		replace := orDefault(fe.Replace, cfg.ReplaceTemplate, "{new_version}")
		oldChunk := substitute(search, cfg.CurrentVersion, next)
		newChunk := substitute(replace, cfg.CurrentVersion, next)

		path := filepath.Join(root, fe.Filename)
		found, err := chunkPresent(path, oldChunk)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		plan.FileChanges = append(plan.FileChanges, FileChange{
			Path:       path,
			OldChunk:   oldChunk,
			NewChunk:   newChunk,
			Found:      found,
			ReplaceAll: fe.ReplaceAll,
		})
	}
	plan.CommitMessage = substitute(orDefault(cfg.MessageTemplate, "",
		"Bump version: {current_version} → {new_version}"), cfg.CurrentVersion, next)
	plan.TagName = substitute(orDefault(cfg.TagNameTemplate, "", "v{new_version}"), cfg.CurrentVersion, next)
	return plan, nil
}

// Apply writes the file changes from plan.  Returns ErrMissingVersion
// if any FileChange has Found == false, mirroring bump-my-version's
// strict default.
//
// Apply does NOT commit, tag, or push — those steps live in a separate
// git helper so this package stays free of git side-effects.
func Apply(plan *Plan) error {
	for _, change := range plan.FileChanges {
		if !change.Found {
			return fmt.Errorf("%w: search string not found in %s",
				ErrMissingVersion, change.Path)
		}
	}
	for _, change := range plan.FileChanges {
		data, err := os.ReadFile(change.Path)
		if err != nil {
			return err
		}
		n := 1
		if change.ReplaceAll {
			n = -1
		}
		updated := strings.Replace(string(data), change.OldChunk, change.NewChunk, n)
		if updated == string(data) {
			// Re-check protects against a race between Compute and Apply.
			return fmt.Errorf("%w: search string disappeared from %s before apply",
				ErrMissingVersion, change.Path)
		}
		if err := os.WriteFile(change.Path, []byte(updated), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// ErrMissingVersion is returned by Apply (or surfaced via Compute when
// Found == false) when one of the configured search strings is absent
// from its target file.
var ErrMissingVersion = errors.New("bump search string not found")

// bumpSemver computes the next semver string for kind from current.
// Only MAJOR.MINOR.PATCH form is supported in v1; pre-release and
// build-metadata suffixes are stripped (the new version drops them).
func bumpSemver(current string, kind Kind) (string, error) {
	current = strings.TrimPrefix(current, "v")
	core := current
	if i := strings.IndexAny(current, "-+"); i >= 0 {
		core = current[:i]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("current_version %q is not MAJOR.MINOR.PATCH", current)
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return "", fmt.Errorf("current_version %q: component %q is not numeric", current, p)
		}
		if n < 0 {
			return "", fmt.Errorf("current_version %q: negative component", current)
		}
		nums[i] = n
	}
	switch kind {
	case Major:
		nums[0]++
		nums[1] = 0
		nums[2] = 0
	case Minor:
		nums[1]++
		nums[2] = 0
	case Patch:
		nums[2]++
	}
	return fmt.Sprintf("%d.%d.%d", nums[0], nums[1], nums[2]), nil
}

func substitute(template, oldV, newV string) string {
	s := strings.ReplaceAll(template, "{current_version}", oldV)
	s = strings.ReplaceAll(s, "{new_version}", newV)
	return s
}

func orDefault(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func chunkPresent(path, chunk string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return strings.Contains(string(data), chunk), nil
}

// PreviewLine returns a one-line human-readable summary of a FileChange
// suitable for a dry-run display.
func (c FileChange) PreviewLine() string {
	if !c.Found {
		return fmt.Sprintf("  %s — NOT FOUND: %q", c.Path, c.OldChunk)
	}
	return fmt.Sprintf("  %s: %q → %q", c.Path, c.OldChunk, c.NewChunk)
}

// semverRE matches a normalized MAJOR.MINOR.PATCH version.  Exposed so
// other packages can validate inputs without re-implementing the rule.
var semverRE = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// IsValid reports whether v is a well-formed MAJOR.MINOR.PATCH string.
func IsValid(v string) bool {
	return semverRE.MatchString(strings.TrimPrefix(v, "v"))
}
