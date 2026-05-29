package cli

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
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
