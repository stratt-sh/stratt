package update

import (
	"context"
	"testing"
)

func TestNewGitHubRequestAuth(t *testing.T) {
	t.Run("unauthenticated when no token", func(t *testing.T) {
		t.Setenv("GH_TOKEN", "")
		t.Setenv("GITHUB_TOKEN", "")
		req, err := newGitHubRequest(context.Background(), "https://api.github.com/repos/o/r/releases/latest")
		if err != nil {
			t.Fatal(err)
		}
		if got := req.Header.Get("Authorization"); got != "" {
			t.Errorf("expected no Authorization header, got %q", got)
		}
	})

	t.Run("GITHUB_TOKEN used when set", func(t *testing.T) {
		t.Setenv("GH_TOKEN", "")
		t.Setenv("GITHUB_TOKEN", "gho_actions")
		req, err := newGitHubRequest(context.Background(), "https://api.github.com/repos/o/r/releases/latest")
		if err != nil {
			t.Fatal(err)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer gho_actions" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer gho_actions")
		}
	})

	t.Run("GH_TOKEN wins over GITHUB_TOKEN (gh CLI precedence)", func(t *testing.T) {
		t.Setenv("GH_TOKEN", "gho_cli")
		t.Setenv("GITHUB_TOKEN", "gho_actions")
		req, err := newGitHubRequest(context.Background(), "https://api.github.com/repos/o/r/releases/latest")
		if err != nil {
			t.Fatal(err)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer gho_cli" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer gho_cli")
		}
	})
}
