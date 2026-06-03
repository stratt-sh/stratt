package cli

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestPromptRootDefault(t *testing.T) {
	var out bytes.Buffer
	in := bufio.NewReader(strings.NewReader("\n"))
	got, err := promptRoot(&out, in)
	if err != nil {
		t.Fatal(err)
	}
	if got != "~/code" {
		t.Errorf("default = %q, want ~/code", got)
	}
}

func TestPromptRootCustom(t *testing.T) {
	var out bytes.Buffer
	in := bufio.NewReader(strings.NewReader("/Volumes/work\n"))
	got, err := promptRoot(&out, in)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/Volumes/work" {
		t.Errorf("got %q", got)
	}
}

func TestPromptLayoutChoices(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"\n", "{host}/{org}/{repo}"},
		{"1\n", "{host}/{org}/{repo}"},
		{"2\n", "{org}/{repo}"},
		{"3\n", "{repo}"},
		{"4\nsrc/{org}/{repo}\n", "src/{org}/{repo}"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			var out bytes.Buffer
			in := bufio.NewReader(strings.NewReader(tc.input))
			got, err := promptLayout(&out, in)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPromptLayoutRejectsBadChoice(t *testing.T) {
	var out bytes.Buffer
	// Garbage, then a valid choice.
	in := bufio.NewReader(strings.NewReader("9\nz\n2\n"))
	got, err := promptLayout(&out, in)
	if err != nil {
		t.Fatal(err)
	}
	if got != "{org}/{repo}" {
		t.Errorf("got %q", got)
	}
	if !strings.Contains(out.String(), "please enter a number from 1 to 4") {
		t.Errorf("expected reprompt; got:\n%s", out.String())
	}
}

func TestPromptCustomLayoutRejectsInvalid(t *testing.T) {
	var out bytes.Buffer
	// Empty, then unknown placeholder, then valid.
	in := bufio.NewReader(strings.NewReader("\n{unknown}/{repo}\n{org}/{repo}\n"))
	got, err := promptCustomLayout(&out, in)
	if err != nil {
		t.Fatal(err)
	}
	if got != "{org}/{repo}" {
		t.Errorf("got %q", got)
	}
	if !strings.Contains(out.String(), "cannot be empty") {
		t.Errorf("expected empty-layout reprompt; got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "invalid layout") {
		t.Errorf("expected invalid-layout reprompt; got:\n%s", out.String())
	}
}

func TestPromptProtocolChoices(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"\n", "ssh"}, // default
		{"1\n", "ssh"},
		{"2\n", "https"},
		{"ssh\n", "ssh"},     // by name
		{"HTTPS\n", "https"}, // name, case-insensitive
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			var out bytes.Buffer
			in := bufio.NewReader(strings.NewReader(tc.input))
			got, err := promptProtocol(&out, in)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPromptProtocolRejectsBadChoice(t *testing.T) {
	var out bytes.Buffer
	in := bufio.NewReader(strings.NewReader("9\nnope\n2\n"))
	got, err := promptProtocol(&out, in)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https" {
		t.Errorf("got %q, want https", got)
	}
	if !strings.Contains(out.String(), "please enter 1 or 2") {
		t.Errorf("expected reprompt; got:\n%s", out.String())
	}
}

func TestEndsWithGit(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://github.com/org/repo.git", true},
		{"https://github.com/org/repo.git/", true},
		{"git@github.com:org/repo.git", true},
		{"https://github.com/org/repo", false},
		{"git@github.com:org/repo", false},
	}
	for _, tc := range cases {
		if got := endsWithGit(tc.in); got != tc.want {
			t.Errorf("endsWithGit(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestResolveCloneRewritesBareURL(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	t.Setenv("STRATT_CONFIG", cfg)
	t.Setenv("HOME", dir) // root "~/code" expands under here
	body := "[workspace]\nroot = \"~/code\"\nlayout = \"{host}/{org}/{repo}\"\nprotocol = \"ssh\"\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{}
	gotURL, gotTarget, err := resolveClone(cmd, "https://github.com/stratt-sh/stratt", "")
	if err != nil {
		t.Fatal(err)
	}
	if gotURL != "git@github.com:stratt-sh/stratt.git" {
		t.Errorf("cloneURL = %q, want ssh form", gotURL)
	}
	wantTarget := filepath.Join(dir, "code", "github.com", "stratt-sh", "stratt")
	if gotTarget != wantTarget {
		t.Errorf("target = %q, want %q", gotTarget, wantTarget)
	}
}

func TestResolveCloneDotGitPassesThrough(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	t.Setenv("STRATT_CONFIG", cfg)
	t.Setenv("HOME", dir)
	body := "[workspace]\nroot = \"~/code\"\nlayout = \"{org}/{repo}\"\nprotocol = \"https\"\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// A .git URL is the user's explicit choice — clone verbatim even
	// though the configured protocol differs.
	cmd := &cobra.Command{}
	gotURL, gotTarget, err := resolveClone(cmd, "git@github.com:stratt-sh/stratt.git", "")
	if err != nil {
		t.Fatal(err)
	}
	if gotURL != "git@github.com:stratt-sh/stratt.git" {
		t.Errorf("cloneURL = %q, want unchanged", gotURL)
	}
	if want := filepath.Join(dir, "code", "stratt-sh", "stratt"); gotTarget != want {
		t.Errorf("target = %q, want %q", gotTarget, want)
	}
}

func TestResolveCloneExplicitTargetDotGitNeedsNoConfig(t *testing.T) {
	// No STRATT_CONFIG set to anything real: escape hatch must not touch config.
	t.Setenv("STRATT_CONFIG", filepath.Join(t.TempDir(), "absent.toml"))
	cmd := &cobra.Command{}
	gotURL, gotTarget, err := resolveClone(cmd, "https://github.com/a/b.git", "/tmp/dest")
	if err != nil {
		t.Fatal(err)
	}
	if gotURL != "https://github.com/a/b.git" || gotTarget != "/tmp/dest" {
		t.Errorf("got (%q, %q), want passthrough", gotURL, gotTarget)
	}
}
