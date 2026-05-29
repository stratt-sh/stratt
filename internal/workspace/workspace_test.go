package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRemote(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Remote
	}{
		{"https plain", "https://github.com/stratt-sh/stratt", Remote{"github.com", "stratt-sh", "stratt"}},
		{"https .git", "https://github.com/stratt-sh/stratt.git", Remote{"github.com", "stratt-sh", "stratt"}},
		{"https trailing slash", "https://github.com/stratt-sh/stratt/", Remote{"github.com", "stratt-sh", "stratt"}},
		{"http", "http://example.com/Org/Repo", Remote{"example.com", "Org", "Repo"}},
		{"ssh scp form", "git@github.com:stratt-sh/stratt.git", Remote{"github.com", "stratt-sh", "stratt"}},
		{"ssh scp no .git", "git@gitlab.com:foo/bar", Remote{"gitlab.com", "foo", "bar"}},
		{"ssh scheme", "ssh://git@github.com/foo/bar.git", Remote{"github.com", "foo", "bar"}},
		{"host uppercased", "https://GitHub.com/foo/bar", Remote{"github.com", "foo", "bar"}},
		{"gitlab subgroup collapses to last two", "https://gitlab.com/group/sub/repo.git", Remote{"gitlab.com", "sub", "repo"}},
		{"whitespace", "  https://github.com/foo/bar  ", Remote{"github.com", "foo", "bar"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRemote(tc.in)
			if err != nil {
				t.Fatalf("ParseRemote(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseRemote(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseRemoteErrors(t *testing.T) {
	bad := []string{
		"",
		"   ",
		"not-a-url",
		"https://github.com/onlyorg",
		"https://github.com/",
		"git@github.com:onlyorg",
		"https:///foo/bar",
	}
	for _, in := range bad {
		t.Run(in, func(t *testing.T) {
			if _, err := ParseRemote(in); err == nil {
				t.Errorf("ParseRemote(%q): expected error, got nil", in)
			}
		})
	}
}

func TestRender(t *testing.T) {
	r := Remote{Host: "github.com", Org: "stratt-sh", Repo: "stratt"}
	cases := map[string]string{
		"{host}/{org}/{repo}":     "github.com/stratt-sh/stratt",
		"{org}/{repo}":            "stratt-sh/stratt",
		"src/{host}/{org}/{repo}": "src/github.com/stratt-sh/stratt",
	}
	for layout, want := range cases {
		got, err := Render(layout, r)
		if err != nil {
			t.Fatalf("Render(%q): %v", layout, err)
		}
		if got != want {
			t.Errorf("Render(%q) = %q, want %q", layout, got, want)
		}
	}
}

func TestRenderErrors(t *testing.T) {
	r := Remote{Host: "github.com", Org: "o", Repo: "r"}
	bad := []string{
		"",
		"{unknown}/{org}",
		"{org}/{repo",
	}
	for _, layout := range bad {
		if _, err := Render(layout, r); err == nil {
			t.Errorf("Render(%q): expected error", layout)
		}
	}
}

func TestResolveHomeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	r := Remote{Host: "github.com", Org: "foo", Repo: "bar"}
	got, err := Resolve("~/code", DefaultLayout, r)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "code", "github.com", "foo", "bar")
	if got != want {
		t.Errorf("Resolve home: got %q, want %q", got, want)
	}
}

func TestResolveEnvExpansion(t *testing.T) {
	t.Setenv("STRATT_TEST_ROOT", "/tmp/stratt-test-root")
	r := Remote{Host: "github.com", Org: "foo", Repo: "bar"}
	got, err := Resolve("$STRATT_TEST_ROOT", "{org}/{repo}", r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, filepath.Join("stratt-test-root", "foo", "bar")) {
		t.Errorf("Resolve env: got %q", got)
	}
}

func TestResolveEmptyRoot(t *testing.T) {
	if _, err := Resolve("", DefaultLayout, Remote{"github.com", "o", "r"}); err == nil {
		t.Error("expected error for empty root")
	}
}
