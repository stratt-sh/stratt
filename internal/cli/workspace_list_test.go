package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRepoDescription(t *testing.T) {
	cases := []struct {
		name   string
		readme string // "" means no README file
		want   string
	}{
		{
			name:   "blockquote tagline preferred",
			readme: "# stratt\n\n> One set of commands for every repo.\n\n## Install\n",
			want:   "One set of commands for every repo.",
		},
		{
			name:   "intro prose under H1",
			readme: "# proj\n\nA small tool that does a thing.\n\n## Usage\n",
			want:   "A small tool that does a thing.",
		},
		{
			name:   "second heading falls back to H1 name",
			readme: "# lcg-ansible\n\n## Requirements\n\nInstall the prerequisites.\n",
			want:   "lcg-ansible",
		},
		{
			name:   "badges skipped before tagline",
			readme: "# proj\n\n[![CI](https://shields.io/x)](y)\n\n> The real description.\n",
			want:   "The real description.",
		},
		{
			name:   "heading-only readme yields the name",
			readme: "# just-a-name\n",
			want:   "just-a-name",
		},
		{
			name:   "no readme yields empty",
			readme: "",
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.readme != "" {
				if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(tc.readme), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := repoDescription(dir); got != tc.want {
				t.Errorf("repoDescription() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRepoDescriptionCaseInsensitiveName — a lowercase readme.md is found
// just like README.md (macOS is case-insensitive, Linux is not).
func TestRepoDescriptionCaseInsensitiveName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "readme.markdown"), []byte("> tagline here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := repoDescription(dir); got != "tagline here" {
		t.Errorf("repoDescription() = %q, want %q", got, "tagline here")
	}
}

// TestWorkspaceListShowsReposAndDescriptions — `workspace list` walks the
// configured root and prints each repo's relative path and description.
func TestWorkspaceListShowsReposAndDescriptions(t *testing.T) {
	root := t.TempDir()
	mkRepo(t, root, "github.com/acme/widget", "# widget\n\n> Builds widgets.\n")
	mkRepo(t, root, "github.com/acme/gadget", "# gadget\n")
	withUserConfig(t, "[workspace]\nroot = "+tomlString(root)+"\n")

	out := runListCmd(t)
	for _, want := range []string{
		"2 repos under",
		"github.com/acme/widget", "Builds widgets.",
		"github.com/acme/gadget",
	} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("workspace list missing %q\n--- output ---\n%s", want, out)
		}
	}
}

// TestWorkspaceListEmptyRoot — an existing but repo-less root reports
// "no git repositories" rather than erroring.
func TestWorkspaceListEmptyRoot(t *testing.T) {
	root := t.TempDir()
	withUserConfig(t, "[workspace]\nroot = "+tomlString(root)+"\n")
	if out := runListCmd(t); !bytes.Contains([]byte(out), []byte("No git repositories")) {
		t.Errorf("expected empty-root message, got:\n%s", out)
	}
}

func runListCmd(t *testing.T) string {
	t.Helper()
	cmd := newWorkspaceListCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// mkRepo creates a repo at root/rel (a dir with a .git marker and the
// given README) so FindRepos discovers it.
func mkRepo(t *testing.T, root, rel, readme string) {
	t.Helper()
	dir := filepath.Join(root, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if readme != "" {
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(readme), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// withUserConfig writes body to a temp ~/.stratt/config.toml and points
// STRATT_CONFIG at it for the duration of the test.
func withUserConfig(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STRATT_CONFIG", path)
}

// tomlString quotes s as a TOML basic string (paths can contain no
// backslashes on the platforms we test, so simple quoting suffices).
func tomlString(s string) string {
	return `"` + s + `"`
}
