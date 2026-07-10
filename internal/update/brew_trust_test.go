package update

import "testing"

func TestBrewMajorVersion(t *testing.T) {
	tests := []struct {
		out   string
		major int
		ok    bool
	}{
		{"Homebrew 6.0.9\n", 6, true},
		{"Homebrew 4.4.15-64-gd0f3f0d\n", 4, true},
		{"Homebrew 10.1.0\nHomebrew/homebrew-core (git revision abc)\n", 10, true},
		{"", 0, false},
		{"brew: command not found", 0, false},
		{"Homebrew", 0, false},
	}
	for _, tt := range tests {
		major, ok := brewMajorVersion(tt.out)
		if major != tt.major || ok != tt.ok {
			t.Errorf("brewMajorVersion(%q) = (%d, %v), want (%d, %v)", tt.out, major, ok, tt.major, tt.ok)
		}
	}
}

func TestParseBrewTrust(t *testing.T) {
	const tap = "stratt-sh/tap"
	const cask = "stratt-sh/tap/stratt"

	tests := []struct {
		name string
		data string
		want BrewTrust
	}{
		{
			name: "tap trusted (real brew 6.0.9 format)",
			data: `{"trustedtaps": ["stratt-sh/tap"]}`,
			want: BrewTrustTrusted,
		},
		{
			name: "cask trusted directly",
			data: `{"trustedtaps": [], "trustedcasks": ["stratt-sh/tap/stratt"]}`,
			want: BrewTrustTrusted,
		},
		{
			name: "other taps only",
			data: `{"trustedtaps": ["1password/tap", "nats-io/nats-tools"]}`,
			want: BrewTrustUntrusted,
		},
		{
			name: "empty registry",
			data: `{}`,
			want: BrewTrustUntrusted,
		},
		{
			name: "corrupt file",
			data: `not json`,
			want: BrewTrustUnknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseBrewTrust([]byte(tt.data), tap, cask); got != tt.want {
				t.Errorf("parseBrewTrust(%q) = %v, want %v", tt.data, got, tt.want)
			}
		})
	}
}
