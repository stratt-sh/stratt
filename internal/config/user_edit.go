package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SetUserWorkspace writes a `[workspace]` section into the user config
// file at path.  Creates parent directories and the file itself if
// missing.  When the file already exists, the new section is appended
// verbatim so any existing user comments are preserved (round-tripping
// through a TOML marshaller would strip them).
//
// The caller must ensure no `[workspace]` section already exists in the
// file; the function is intended for first-run setup where the section
// is absent.
func SetUserWorkspace(path, root, layout string) error {
	if root == "" {
		return fmt.Errorf("workspace root is required")
	}
	if layout == "" {
		return fmt.Errorf("workspace layout is required")
	}

	section := fmt.Sprintf("[workspace]\nroot = %q\nlayout = %q\n", root, layout)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	var body string
	switch {
	case len(existing) == 0:
		body = section
	case strings.HasSuffix(string(existing), "\n\n"):
		body = string(existing) + section
	case strings.HasSuffix(string(existing), "\n"):
		body = string(existing) + "\n" + section
	default:
		body = string(existing) + "\n\n" + section
	}

	if err := os.WriteFile(path+".tmp", []byte(body), 0o644); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
