// Package workspace resolves where a freshly-cloned repo should live
// on disk, based on the user's `[workspace]` configuration.
//
// Stratt does not wrap `git` itself — a Go binary can't intercept
// `git clone` invocations.  Instead, `stratt clone` parses the URL,
// renders a layout template against the user's configured root, and
// shells out to `git clone <url> <target>`.  See cmd/stratt clone.
package workspace

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Remote is the parsed identity of a git remote.  Host is lower-cased
// (github.com, gitlab.com) so layouts stay stable across input casing.
// Org is the owner segment (a user or organization).  Repo has any
// trailing `.git` stripped.
type Remote struct {
	Host string
	Org  string
	Repo string
}

// ParseRemote accepts the URL forms `git clone` itself accepts:
//
//	https://github.com/org/repo
//	https://github.com/org/repo.git
//	http://host/org/repo
//	git@github.com:org/repo.git
//	ssh://git@github.com/org/repo.git
//
// It deliberately does NOT support local paths or the rarer
// `user@host:path` forms with deep paths — `stratt clone` is for
// cloning canonical hosted repos into the workspace, and anything
// exotic should be cloned with `git` directly.
func ParseRemote(raw string) (Remote, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Remote{}, fmt.Errorf("empty url")
	}

	// scp-style: git@github.com:org/repo.git
	// Detect by a colon that precedes any slash, with no scheme.
	if !hasScheme(s) {
		if i := strings.IndexByte(s, ':'); i >= 0 {
			head := s[:i]
			tail := s[i+1:]
			host := head
			if at := strings.IndexByte(head, '@'); at >= 0 {
				host = head[at+1:]
			}
			return splitOrgRepo(host, tail)
		}
		return Remote{}, fmt.Errorf("unrecognized url: %s", raw)
	}

	u, err := url.Parse(s)
	if err != nil {
		return Remote{}, fmt.Errorf("parse url: %w", err)
	}
	if u.Host == "" {
		return Remote{}, fmt.Errorf("url missing host: %s", raw)
	}
	return splitOrgRepo(u.Host, u.Path)
}

func hasScheme(s string) bool {
	i := strings.Index(s, "://")
	if i < 0 {
		return false
	}
	for _, c := range s[:i] {
		if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && c != '+' && c != '-' && c != '.' {
			return false
		}
	}
	return true
}

func splitOrgRepo(host, path string) (Remote, error) {
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return Remote{}, fmt.Errorf("expected org/repo, got %q", path)
	}
	// Take the last two segments so subgroup-style URLs (gitlab)
	// still produce a usable {org, repo}.  We collapse the org to a
	// single segment — this is "good enough" for the GitHub-centric
	// workflow stratt clone targets, and users with deep gitlab
	// hierarchies can clone manually.
	org := parts[len(parts)-2]
	repo := strings.TrimSuffix(parts[len(parts)-1], ".git")
	if repo == "" {
		return Remote{}, fmt.Errorf("empty repo name in %q", path)
	}
	return Remote{
		Host: strings.ToLower(host),
		Org:  org,
		Repo: repo,
	}, nil
}

// Render expands a layout template against a Remote.  Supported
// placeholders: {host}, {org}, {repo}.  An unknown placeholder is a
// load-time error rather than a silent passthrough so typos fail loudly.
func Render(layout string, r Remote) (string, error) {
	if layout == "" {
		return "", fmt.Errorf("empty layout")
	}
	var out strings.Builder
	i := 0
	for i < len(layout) {
		c := layout[i]
		if c != '{' {
			out.WriteByte(c)
			i++
			continue
		}
		end := strings.IndexByte(layout[i:], '}')
		if end < 0 {
			return "", fmt.Errorf("unterminated placeholder in layout %q", layout)
		}
		name := layout[i+1 : i+end]
		switch name {
		case "host":
			out.WriteString(r.Host)
		case "org":
			out.WriteString(r.Org)
		case "repo":
			out.WriteString(r.Repo)
		default:
			return "", fmt.Errorf("unknown layout placeholder {%s}", name)
		}
		i += end + 1
	}
	return out.String(), nil
}

// Resolve returns the absolute target directory for a clone given a
// root, layout, and remote.  `~` and `$VAR` in root are expanded.  The
// returned path is cleaned but not created; callers do mkdir.
func Resolve(root, layout string, r Remote) (string, error) {
	base, err := ExpandRoot(root)
	if err != nil {
		return "", err
	}
	rendered, err := Render(layout, r)
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(base, rendered))
}

// ExpandRoot expands `$VAR` and a leading `~` in a workspace root and
// returns an absolute, cleaned path.  An empty root is an error so
// callers fail loudly rather than scanning the current directory.
func ExpandRoot(root string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("workspace root is unset")
	}
	expanded, err := expandHome(os.ExpandEnv(root))
	if err != nil {
		return "", err
	}
	return filepath.Abs(expanded)
}

// FindRepos walks the workspace root and returns the absolute paths of
// every git repository beneath it — any directory containing a `.git`
// entry (a directory for a normal checkout, or a file for a worktree or
// submodule).  Results are sorted.
//
// Once a repository is found, FindRepos does not descend into it: nested
// checkouts inside a working tree are considered part of that repo, and
// the `.git` directory itself is never traversed.  Unreadable
// subdirectories are skipped rather than aborting the whole walk.
func FindRepos(root string) ([]string, error) {
	base, err := ExpandRoot(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(base)
	if err != nil {
		return nil, fmt.Errorf("workspace root %s: %w", base, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace root %s is not a directory", base)
	}

	var repos []string
	walkErr := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Permission error or vanished entry: skip this subtree but
			// keep scanning the rest of the workspace.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if isRepo(path) {
			repos = append(repos, path)
			return fs.SkipDir // don't descend into the repo's working tree
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Strings(repos)
	return repos, nil
}

// isRepo reports whether dir contains a `.git` entry of any kind.
func isRepo(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

func expandHome(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

// DefaultLayout is used when the user sets [workspace] root but leaves
// layout empty.  Matches `ghq`'s default and keeps repos addressable
// across hosts.
const DefaultLayout = "{host}/{org}/{repo}"
