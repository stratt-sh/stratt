package cli

import "testing"

func TestBrewTapOf(t *testing.T) {
	tests := []struct {
		formula string
		want    string
	}{
		{"stratt-sh/tap/stratt", "stratt-sh/tap"},
		{"owner/repo/name", "owner/repo"},
		{"stratt", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := brewTapOf(tt.formula); got != tt.want {
			t.Errorf("brewTapOf(%q) = %q, want %q", tt.formula, got, tt.want)
		}
	}
}

func TestIsUntrustedTapError(t *testing.T) {
	// Real Homebrew 6.0.9 refusal output.
	refusal := "Warning: Skipping stratt-sh/tap because it is not trusted. Run `brew trust stratt-sh/tap` to trust it.\n" +
		"Error: Refusing to load cask stratt-sh/tap/stratt from untrusted tap stratt-sh/tap.\n" +
		"Run `brew trust --cask stratt-sh/tap/stratt` or `brew trust stratt-sh/tap` to trust it."
	if !isUntrustedTapError(refusal) {
		t.Error("expected Homebrew tap-trust refusal to be detected")
	}

	for _, benign := range []string{
		"",
		"Error: stratt not installed",
		"==> Upgrading stratt-sh/tap/stratt",
	} {
		if isUntrustedTapError(benign) {
			t.Errorf("false positive on %q", benign)
		}
	}
}
