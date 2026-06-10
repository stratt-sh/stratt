package kustomize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeOverlay(t *testing.T, root, env, body string) string {
	t.Helper()
	path := OverlayPath(root, env)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSetImageSingleImageNoNameArg(t *testing.T) {
	dir := t.TempDir()
	path := writeOverlay(t, dir, "prod", `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - ../../base
images:
  - name: myapp
    newTag: 1.14.0
`)
	change, err := SetImage(path, "", "1.14.1")
	if err != nil {
		t.Fatal(err)
	}
	if change.Image != "myapp" || change.OldTag != "1.14.0" || change.NewTag != "1.14.1" {
		t.Errorf("change: %+v", change)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "newTag: 1.14.1") {
		t.Errorf("file not updated:\n%s", body)
	}
}

// TestSetImagePreservesIndentAndComments — the edit must not re-indent
// the file or drop comments.  Regression: yaml.Marshal defaults to a
// 4-space indent, so a standard 2-space overlay came back fully
// re-indented on every `stratt deploy`.
func TestSetImagePreservesIndentAndComments(t *testing.T) {
	dir := t.TempDir()
	path := writeOverlay(t, dir, "prod", `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
# pin the app image
images:
  - name: myapp
    newTag: 1.14.0
`)
	if _, err := SetImage(path, "myapp", "1.14.1"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "  - name: myapp") {
		t.Errorf("2-space indent not preserved:\n%s", body)
	}
	if strings.Contains(string(body), "    - name: myapp") {
		t.Errorf("file was re-indented to 4 spaces:\n%s", body)
	}
	if !strings.Contains(string(body), "# pin the app image") {
		t.Errorf("comment dropped:\n%s", body)
	}
}

func TestSetImageNamedTarget(t *testing.T) {
	dir := t.TempDir()
	path := writeOverlay(t, dir, "prod", `images:
  - name: foo
    newTag: 1.0.0
  - name: bar
    newTag: 2.0.0
`)
	change, err := SetImage(path, "bar", "2.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if change.Image != "bar" || change.OldTag != "2.0.0" || change.NewTag != "2.1.0" {
		t.Errorf("change: %+v", change)
	}
	body, _ := os.ReadFile(path)
	// `foo` should still be 1.0.0; only `bar` updated.
	if !strings.Contains(string(body), "newTag: 1.0.0") {
		t.Error("foo image was touched unexpectedly")
	}
	if !strings.Contains(string(body), "newTag: 2.1.0") {
		t.Error("bar image not updated")
	}
}

func TestSetImageMultipleImagesNoNameErrors(t *testing.T) {
	dir := t.TempDir()
	path := writeOverlay(t, dir, "prod", `images:
  - name: foo
    newTag: 1.0.0
  - name: bar
    newTag: 2.0.0
`)
	_, err := SetImage(path, "", "9.9.9")
	if err == nil {
		t.Fatal("expected error for ambiguous primary image")
	}
	if !strings.Contains(err.Error(), "multiple images") {
		t.Errorf("got %v", err)
	}
}

func TestSetImageUnknownImageErrors(t *testing.T) {
	dir := t.TempDir()
	path := writeOverlay(t, dir, "prod", `images:
  - name: foo
    newTag: 1.0.0
`)
	_, err := SetImage(path, "missing", "1.0.0")
	if err == nil {
		t.Fatal("expected error for unknown image")
	}
}

// TestSetImageUnknownImageSuggestsSingle — when the overlay has exactly
// one image and a non-matching --image/primary_image is given, the error
// names the actual image so the misconfiguration is self-diagnosing.
func TestSetImageUnknownImageSuggestsSingle(t *testing.T) {
	dir := t.TempDir()
	path := writeOverlay(t, dir, "prod", `images:
  - name: ghcr.io/zebpalmer/emulsia
    newTag: 1.0.0
`)
	_, err := SetImage(path, "emulsia", "1.1.0")
	if err == nil {
		t.Fatal("expected error for unknown image")
	}
	if !strings.Contains(err.Error(), `did you mean "ghcr.io/zebpalmer/emulsia"?`) {
		t.Errorf("error should suggest the actual image; got %v", err)
	}
}

// TestSetImageUnknownImageAmongManySuggestsSubstring — with several images,
// a substring of one is suggested; otherwise the available set is listed.
func TestSetImageUnknownImageAmongManySuggestsSubstring(t *testing.T) {
	dir := t.TempDir()
	path := writeOverlay(t, dir, "prod", `images:
  - name: ghcr.io/org/api
    newTag: 1.0.0
  - name: ghcr.io/org/web
    newTag: 1.0.0
`)
	_, err := SetImage(path, "api", "1.1.0")
	if err == nil || !strings.Contains(err.Error(), `did you mean "ghcr.io/org/api"?`) {
		t.Errorf("substring should suggest ghcr.io/org/api; got %v", err)
	}

	_, err = SetImage(path, "zzz", "1.1.0")
	if err == nil || !strings.Contains(err.Error(), "available: ghcr.io/org/api, ghcr.io/org/web") {
		t.Errorf("no match should list available images; got %v", err)
	}
}

func TestSetImageNoImagesSectionErrors(t *testing.T) {
	dir := t.TempDir()
	path := writeOverlay(t, dir, "prod", `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - ../../base
`)
	_, err := SetImage(path, "", "1.0.0")
	if err == nil {
		t.Fatal("expected error for missing images section")
	}
}

// TestSetImageCreatesNewTagIfAbsent — entries with only `name:` but no
// existing `newTag` should get one inserted.  This matches kustomize's
// own behavior.
func TestSetImageCreatesNewTagIfAbsent(t *testing.T) {
	dir := t.TempDir()
	path := writeOverlay(t, dir, "prod", `images:
  - name: foo
`)
	change, err := SetImage(path, "foo", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if change.OldTag != "" || change.NewTag != "1.0.0" {
		t.Errorf("change: %+v", change)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "newTag: 1.0.0") {
		t.Errorf("expected newTag inserted; got:\n%s", body)
	}
}

// TestSetImagePreservesOtherContent — fields outside the targeted image
// and comments should remain intact.  yaml.v3 Node round-tripping
// preserves these.
func TestSetImagePreservesOtherContent(t *testing.T) {
	dir := t.TempDir()
	original := `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: production
resources:
  - ../../base
  - ingress.yaml
configMapGenerator:
  - name: app-config
    literals:
      - LOG_LEVEL=info
images:
  - name: myapp
    newTag: 1.14.0
`
	path := writeOverlay(t, dir, "prod", original)
	if _, err := SetImage(path, "myapp", "1.14.1"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	got := string(body)

	// Everything except the tag should survive.
	for _, fragment := range []string{
		"namespace: production",
		"- ingress.yaml",
		"LOG_LEVEL=info",
		"name: myapp",
	} {
		if !strings.Contains(got, fragment) {
			t.Errorf("lost content %q:\n%s", fragment, got)
		}
	}
	if !strings.Contains(got, "newTag: 1.14.1") {
		t.Errorf("tag not updated:\n%s", got)
	}
	if strings.Contains(got, "newTag: 1.14.0") {
		t.Errorf("old tag still present:\n%s", got)
	}
}

func TestOverlayPath(t *testing.T) {
	got := OverlayPath("/repo", "prod")
	want := filepath.Join("/repo", "deploy", "overlays", "prod", "kustomization.yaml")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
