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
	"net/url"
	"os"
	"path/filepath"
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
	if root == "" {
		return "", fmt.Errorf("workspace root is unset")
	}
	rendered, err := Render(layout, r)
	if err != nil {
		return "", err
	}
	expanded, err := expandHome(os.ExpandEnv(root))
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(filepath.Join(expanded, rendered))
	if err != nil {
		return "", err
	}
	return abs, nil
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
