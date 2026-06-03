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

// SetUserWorkspaceProtocol records the preferred clone protocol ("ssh"
// or "https") in the `[workspace]` section of the user config at path.
//
// Unlike SetUserWorkspace, the `[workspace]` section may already exist
// (with root/layout but no protocol): the new key is inserted directly
// under the existing header so surrounding keys and comments are
// preserved.  If no `[workspace]` section exists yet, one is appended.
// An existing `protocol` key in the section is replaced in place.
func SetUserWorkspaceProtocol(path, protocol string) error {
	if protocol == "" {
		return fmt.Errorf("clone protocol is required")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	body := upsertWorkspaceProtocol(string(existing), protocol)

	if err := os.WriteFile(path+".tmp", []byte(body), 0o644); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// upsertWorkspaceProtocol returns existing with a `protocol = "..."` key
// set inside its `[workspace]` section: replacing an existing key,
// inserting under an existing header, or appending a fresh section.
func upsertWorkspaceProtocol(existing, protocol string) string {
	line := fmt.Sprintf("protocol = %q", protocol)
	lines := strings.Split(existing, "\n")

	for i, l := range lines {
		if strings.TrimSpace(l) != "[workspace]" {
			continue
		}
		// Scan the section body for an existing protocol key to replace.
		for j := i + 1; j < len(lines); j++ {
			t := strings.TrimSpace(lines[j])
			if strings.HasPrefix(t, "[") {
				break // next section starts; key not found
			}
			if t == "protocol" || strings.HasPrefix(t, "protocol ") || strings.HasPrefix(t, "protocol=") {
				lines[j] = line
				return strings.Join(lines, "\n")
			}
		}
		// No protocol key: insert right after the header.
		out := append([]string{}, lines[:i+1]...)
		out = append(out, line)
		out = append(out, lines[i+1:]...)
		return strings.Join(out, "\n")
	}

	// No [workspace] section: append one, matching SetUserWorkspace's
	// trailing-newline handling so the file stays tidy.
	section := "[workspace]\n" + line + "\n"
	switch {
	case len(existing) == 0:
		return section
	case strings.HasSuffix(existing, "\n\n"):
		return existing + section
	case strings.HasSuffix(existing, "\n"):
		return existing + "\n" + section
	default:
		return existing + "\n\n" + section
	}
}
