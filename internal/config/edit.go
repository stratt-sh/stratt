package config

import (
	"fmt"
	"os"
	"strings"
)

// SetRequiredStratt writes `required_stratt = "<value>"` into the project
// config file at path.  Detects whether path is a pyproject.toml (writes
// to [tool.stratt]) or a stratt.toml (writes to the top level) by the
// filename.
//
// Used by `stratt config require-version` (R2.3.13) and by
// `stratt config migrate` when bumping the pin after a successful migration.
//
// The edit is line-surgical — find/replace the single key, or insert it —
// rather than round-tripping the whole document through a TOML marshaller,
// so the user's existing comments and key ordering survive.
func SetRequiredStratt(path, constraint string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("required_stratt = %q", constraint)

	var body string
	if strings.HasSuffix(path, "pyproject.toml") {
		body = upsertSectionKey(string(data), "tool.stratt", "required_stratt", line)
	} else {
		body = upsertTopLevelKey(string(data), "required_stratt", line)
	}

	if err := os.WriteFile(path+".tmp", []byte(body), 0o644); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// keyMatches reports whether a trimmed line declares the given bare key
// (`key`, `key =`, or `key=`), not merely a key with this as a prefix.
func keyMatches(trimmed, key string) bool {
	return trimmed == key ||
		strings.HasPrefix(trimmed, key+" ") ||
		strings.HasPrefix(trimmed, key+"=")
}

// upsertTopLevelKey sets a top-level key in a TOML document: replacing it
// where it already appears (before the first table header), inserting it
// just before the first table, or appending it to a table-less document.
func upsertTopLevelKey(existing, key, line string) string {
	lines := strings.Split(existing, "\n")
	firstTable := -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") {
			firstTable = i
			break
		}
		if keyMatches(t, key) {
			lines[i] = line
			return strings.Join(lines, "\n")
		}
	}
	if firstTable < 0 {
		// No tables at all — append at the end (still top-level).
		return appendBlock(existing, line+"\n")
	}
	// Insert at the end of the top-level region, just before the table.
	out := append([]string{}, lines[:firstTable]...)
	out = append(out, line)
	out = append(out, lines[firstTable:]...)
	return strings.Join(out, "\n")
}

// upsertSectionKey sets key inside the [section] table: replacing it in
// place, inserting it directly under an existing header, or appending a
// fresh section when the table is absent.
func upsertSectionKey(existing, section, key, line string) string {
	header := "[" + section + "]"
	lines := strings.Split(existing, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != header {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			t := strings.TrimSpace(lines[j])
			if strings.HasPrefix(t, "[") {
				break // next table starts; key not found in this one
			}
			if keyMatches(t, key) {
				lines[j] = line
				return strings.Join(lines, "\n")
			}
		}
		// Header present, key absent: insert right after the header.
		out := append([]string{}, lines[:i+1]...)
		out = append(out, line)
		out = append(out, lines[i+1:]...)
		return strings.Join(out, "\n")
	}
	// No section: append a fresh one.
	return appendBlock(existing, header+"\n"+line+"\n")
}

// appendBlock appends block to existing, normalizing the seam so the file
// keeps a single blank-line separation and a trailing newline.
func appendBlock(existing, block string) string {
	switch {
	case len(existing) == 0:
		return block
	case strings.HasSuffix(existing, "\n\n"):
		return existing + block
	case strings.HasSuffix(existing, "\n"):
		return existing + "\n" + block
	default:
		return existing + "\n\n" + block
	}
}
