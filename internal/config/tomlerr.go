package config

import (
	"errors"
	"fmt"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// enrichTOMLError turns a go-toml decode error into a message that names
// the offending key and line number, instead of the bare "strict mode:
// fields ... are missing" / internal-struct-field text go-toml emits by
// default.  Non-TOML errors pass through unchanged.  Callers keep their
// own file-path context wrapping around the result.
func enrichTOMLError(err error) error {
	if err == nil {
		return nil
	}

	var strict *toml.StrictMissingError
	if errors.As(err, &strict) {
		var b strings.Builder
		b.WriteString("unknown configuration key(s) — stratt rejects unrecognized keys to catch typos:")
		for i := range strict.Errors {
			de := &strict.Errors[i]
			key := strings.Join([]string(de.Key()), ".")
			if key == "" {
				key = de.Error()
			}
			if row, _ := de.Position(); row > 0 {
				fmt.Fprintf(&b, "\n  %s (line %d)", key, row)
			} else {
				fmt.Fprintf(&b, "\n  %s", key)
			}
		}
		return errors.New(b.String())
	}

	var dec *toml.DecodeError
	if errors.As(err, &dec) {
		msg := strings.TrimPrefix(dec.Error(), "toml: ")
		if row, col := dec.Position(); row > 0 {
			return fmt.Errorf("%s (line %d, column %d)", msg, row, col)
		}
		return errors.New(msg)
	}

	return err
}
