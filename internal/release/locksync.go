package release

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/stratt-sh/stratt/internal/bump"
	"github.com/stratt-sh/stratt/internal/detect"
)

// lockSync is one lockfile-regeneration command the release flow must run
// after the bump is applied and before the release commit.  The bump engine
// rewrites manifest version fields (pyproject.toml, package.json) but the
// ecosystem lockfiles also record the project's own version; without a
// re-sync the next `uv sync` / `npm install` leaves a dangling diff that
// pollutes whatever commit comes next.
type lockSync struct {
	// Dir is the directory the command runs in, relative to the repo root
	// ("." for the root itself).
	Dir string
	// Argv is the command to run.
	Argv []string
	// Lockfile is the repo-root-relative path of the file the command
	// rewrites, staged into the release commit afterward.
	Lockfile string
}

// display renders the sync for a progress line, e.g. "uv lock (backend/)".
func (s lockSync) display() string {
	cmd := strings.Join(s.Argv, " ")
	if s.Dir == "." {
		return cmd
	}
	return fmt.Sprintf("%s (%s/)", cmd, s.Dir)
}

// planLockSyncs decides which lockfile syncs a bump plan requires.  It
// considers the repo root plus every declared subproject directory, and
// schedules a sync only where the bump plan actually modified that
// directory's manifest AND the matching lockfile exists (per the detect
// package's stack signals):
//
//   - python+uv:  pyproject.toml modified, uv.lock present  → `uv lock`
//     (preferred over `uv sync` — updates the lockfile without touching
//     any virtualenv; uv owns PEP 440 normalization of the version)
//   - node+npm:   package.json modified, package-lock.json present
//     → `npm install --package-lock-only --ignore-scripts`
func planLockSyncs(root string, subprojects []string, plan *bump.Plan) []lockSync {
	modified := map[string]bool{}
	for _, c := range plan.FileChanges {
		modified[filepath.Clean(c.Path)] = true
	}

	dirs := []string{"."}
	seen := map[string]bool{".": true}
	for _, sp := range subprojects {
		sp = filepath.Clean(sp)
		if sp == "" || seen[sp] {
			continue
		}
		seen[sp] = true
		dirs = append(dirs, sp)
	}

	var syncs []lockSync
	for _, dir := range dirs {
		abs := filepath.Join(root, dir)
		for _, stack := range detect.Scan(abs).Stacks {
			switch stack.Name {
			case "python+uv":
				if modified[filepath.Join(abs, "pyproject.toml")] {
					syncs = append(syncs, lockSync{
						Dir:      dir,
						Argv:     []string{"uv", "lock"},
						Lockfile: filepath.Join(dir, "uv.lock"),
					})
				}
			case "node+npm":
				if modified[filepath.Join(abs, "package.json")] {
					syncs = append(syncs, lockSync{
						Dir:      dir,
						Argv:     []string{"npm", "install", "--package-lock-only", "--ignore-scripts"},
						Lockfile: filepath.Join(dir, "package-lock.json"),
					})
				}
			}
		}
	}
	return syncs
}

// runLockSync executes one sync command in its directory.  A failure —
// tool missing, resolver error, network — aborts the release before the
// commit is created; the caller surfaces the error verbatim.
func runLockSync(ctx context.Context, root string, s lockSync) error {
	cmd := exec.CommandContext(ctx, s.Argv[0], s.Argv[1:]...)
	cmd.Dir = filepath.Join(root, s.Dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"lockfile sync failed in %s/: `%s`: %w\n%s"+
				"the release was aborted before the commit; the bumped files are "+
				"left uncommitted (restore with `git checkout -- .`, or fix the "+
				"tool and re-run `stratt release`)",
			s.Dir, strings.Join(s.Argv, " "), err, indentOutput(out))
	}
	return nil
}

// indentOutput formats captured tool output for inclusion in an error
// message: indented two spaces, with a trailing newline so the guidance
// that follows starts on its own line.  Empty output collapses to nothing.
func indentOutput(out []byte) string {
	text := strings.TrimSpace(string(out))
	if text == "" {
		return ""
	}
	return "  " + strings.ReplaceAll(text, "\n", "\n  ") + "\n"
}
