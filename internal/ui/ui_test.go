package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseColorMode(t *testing.T) {
	cases := map[string]ColorMode{
		"auto":   ColorAuto,
		"":       ColorAuto,
		"AUTO":   ColorAuto,
		"always": ColorAlways,
		"force":  ColorAlways,
		"never":  ColorNever,
		"no":     ColorNever,
		"off":    ColorNever,
		"bogus":  ColorAuto, // fallback
	}
	for in, want := range cases {
		if got := ParseColorMode(in); got != want {
			t.Errorf("ParseColorMode(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestStyleNeverColor(t *testing.T) {
	var buf bytes.Buffer
	s := NewStyle(&buf, &buf, ColorNever, Normal)
	if s.UseColor() {
		t.Error("ColorNever should disable color")
	}
	got := s.Success("done")
	if strings.Contains(got, "\x1b[") {
		t.Errorf("ColorNever should emit no escape codes; got %q", got)
	}
	if !strings.Contains(got, "✓") {
		t.Errorf("Success marker missing: %q", got)
	}
}

func TestStyleAlwaysColor(t *testing.T) {
	var buf bytes.Buffer
	s := NewStyle(&buf, &buf, ColorAlways, Normal)
	if !s.UseColor() {
		t.Error("ColorAlways should enable color")
	}
	got := s.Failure("kaboom")
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("ColorAlways should emit escape codes; got %q", got)
	}
}

func TestStyleAutoDefaultsOffForNonTTY(t *testing.T) {
	var buf bytes.Buffer
	s := NewStyle(&buf, &buf, ColorAuto, Normal)
	if s.UseColor() {
		t.Error("ColorAuto on a buffer (non-TTY) should disable color")
	}
}

// TestNoColorEnvAffectsOnlyAuto — the NO_COLOR convention suppresses
// color under ColorAuto, but an explicit ColorAlways (e.g. `--color
// always`) overrides it, as the flag's help promises.
func TestNoColorEnvAffectsOnlyAuto(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var buf bytes.Buffer

	if NewStyle(&buf, &buf, ColorAuto, Normal).UseColor() {
		t.Error("NO_COLOR should disable color under ColorAuto")
	}
	if !NewStyle(&buf, &buf, ColorAlways, Normal).UseColor() {
		t.Error("ColorAlways should override NO_COLOR")
	}
}

// TestInlineColorHelpers — Green/Red/Yellow/Bold/Faint emit codes when
// color is on and pass text through untouched when it's off.
func TestInlineColorHelpers(t *testing.T) {
	var buf bytes.Buffer
	on := NewStyle(&buf, &buf, ColorAlways, Normal)
	off := NewStyle(&buf, &buf, ColorNever, Normal)
	for _, fn := range []func(string) string{on.Green, on.Red, on.Yellow, on.Bold, on.Faint} {
		if got := fn("x"); !strings.Contains(got, "\x1b[") || !strings.Contains(got, "x") {
			t.Errorf("color-on helper should wrap text in codes; got %q", got)
		}
	}
	for _, fn := range []func(string) string{off.Green, off.Red, off.Yellow, off.Bold, off.Faint} {
		if got := fn("x"); got != "x" {
			t.Errorf("color-off helper should pass text through; got %q", got)
		}
	}
}

func TestStyleMarkers(t *testing.T) {
	var buf bytes.Buffer
	s := NewStyle(&buf, &buf, ColorNever, Normal)
	for _, tc := range []struct {
		name   string
		fn     func(string) string
		marker string
	}{
		{"success", s.Success, "✓"},
		{"failure", s.Failure, "✗"},
		{"progress", s.Progress, "→"},
		{"task", s.Task, "▶"},
		{"warn", s.Warn, "warning:"},
		{"error", s.Error, "error:"},
	} {
		out := tc.fn("hello")
		if !strings.Contains(out, tc.marker) {
			t.Errorf("%s missing marker %q: %q", tc.name, tc.marker, out)
		}
		if !strings.Contains(out, "hello") {
			t.Errorf("%s missing message: %q", tc.name, out)
		}
	}
}
