package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

// User is the schema for ~/.stratt/config.toml (R6.7).  Per R2.3.7,
// the user-config schema is intentionally disjoint from project config:
// project policy goes in stratt.toml / pyproject.toml; per-user
// preferences live here.
//
// Narrow named exception: `[release]` and `[deploy]` accept the same
// `push` / `commit` boolean overrides that the project config does, so
// users can opt out of auto-push without forking every repo.  Project
// settings WIN when both are set (project policy is sticky).
type User struct {
	Source string

	Update    *UserUpdate
	Display   *UserDisplay
	Paths     *UserPaths
	Release   *UserReleaseDefaults
	Deploy    *UserDeployDefaults
	Workspace *UserWorkspace
}

// UserWorkspace configures `stratt clone`'s on-disk layout.  `Root` is
// the directory under which cloned repos are placed (e.g. "~/code").
// `Layout` is a template containing some combination of {host}, {org},
// and {repo}; defaults to "{host}/{org}/{repo}" when empty.  `Protocol`
// is the preferred clone protocol ("ssh" or "https") used to rewrite a
// bare URL into a canonical clone URL; empty means "ask on first use".
type UserWorkspace struct {
	Root     string
	Layout   string
	Protocol string
}

// UserUpdate — per-user update behavior (R6.7).
type UserUpdate struct {
	Channel       string // "default" | "prerelease" (empty = default; "stable" accepted as a deprecated alias)
	CheckInterval string // free-form duration (parsed later); empty = default
	AutoCheck     *bool  // nil = default true; false to disable polling
}

// UserDisplay — color & verbosity preferences.
type UserDisplay struct {
	Color     string // "auto" | "always" | "never"
	Verbosity string // "quiet" | "normal" | "verbose" | "debug"
}

// UserPaths — explicit tool paths so users can override `$PATH` choices
// (e.g. pin a specific `uv` install).
type UserPaths struct {
	Tools map[string]string
}

// UserReleaseDefaults are personal defaults for fields the project
// hasn't pinned.  Mirrors a narrow allowlist of Release fields.
type UserReleaseDefaults struct {
	Push   *bool
	Commit *bool
}

// UserDeployDefaults — same idea for deploy.
type UserDeployDefaults struct {
	Push   *bool
	Commit *bool
}

// UserConfigPath returns ~/.stratt/config.toml.  Honors $STRATT_CONFIG
// for tests and unusual setups.
func UserConfigPath() (string, error) {
	if v := os.Getenv("STRATT_CONFIG"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".stratt", "config.toml"), nil
}

// LoadUser reads ~/.stratt/config.toml (or $STRATT_CONFIG override).
// Returns a zero-value User with no error when the file doesn't exist.
//
// Per R2.3.7 we forbid project-config sections from appearing here.
// A `[tasks]`, `[helpers]`, `[bump]`, or `required_stratt` field in the
// user config is a load error pointing at the misplaced section.
func LoadUser() (*User, error) {
	path, err := UserConfigPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &User{}, nil
	}
	if err != nil {
		return nil, err
	}

	// Strict parse with separation enforcement.
	var raw rawUser
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, enrichTOMLError(err))
	}

	u := &User{Source: path}
	if raw.Update != nil {
		u.Update = &UserUpdate{
			Channel:       raw.Update.Channel,
			CheckInterval: raw.Update.CheckInterval,
			AutoCheck:     raw.Update.AutoCheck,
		}
	}
	if raw.Display != nil {
		u.Display = &UserDisplay{
			Color:     raw.Display.Color,
			Verbosity: raw.Display.Verbosity,
		}
	}
	if len(raw.Paths) > 0 {
		u.Paths = &UserPaths{Tools: raw.Paths}
	}
	if raw.Release != nil {
		u.Release = &UserReleaseDefaults{
			Push:   raw.Release.Push,
			Commit: raw.Release.Commit,
		}
	}
	if raw.Deploy != nil {
		u.Deploy = &UserDeployDefaults{
			Push:   raw.Deploy.Push,
			Commit: raw.Deploy.Commit,
		}
	}
	if raw.Workspace != nil {
		u.Workspace = &UserWorkspace{
			Root:     raw.Workspace.Root,
			Layout:   raw.Workspace.Layout,
			Protocol: raw.Workspace.Protocol,
		}
	}
	return u, nil
}

// rawUser is the on-disk shape of ~/.stratt/config.toml.  Top-level
// fields strictly disjoint from rawProject (R2.3.7) — strict
// unknown-field parsing rejects anything that doesn't fit, so a
// misplaced [tasks] in the user file fails with "unknown field tasks".
type rawUser struct {
	Update    *rawUserUpdate          `toml:"update"`
	Display   *rawUserDisplay         `toml:"display"`
	Paths     map[string]string       `toml:"paths"`
	Release   *rawUserReleaseDefaults `toml:"release"`
	Deploy    *rawUserDeployDefaults  `toml:"deploy"`
	Workspace *rawUserWorkspace       `toml:"workspace"`
}

type rawUserWorkspace struct {
	Root     string `toml:"root"`
	Layout   string `toml:"layout"`
	Protocol string `toml:"protocol"`
}

type rawUserUpdate struct {
	Channel       string `toml:"channel"`
	CheckInterval string `toml:"check_interval"`
	AutoCheck     *bool  `toml:"auto_check"`
}

type rawUserDisplay struct {
	Color     string `toml:"color"`
	Verbosity string `toml:"verbosity"`
}

type rawUserReleaseDefaults struct {
	Push   *bool `toml:"push"`
	Commit *bool `toml:"commit"`
}

type rawUserDeployDefaults struct {
	Push   *bool `toml:"push"`
	Commit *bool `toml:"commit"`
}
