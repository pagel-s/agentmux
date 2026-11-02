package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

// A native frame must have one unambiguous meaning before any field is used
// to choose its answer or outcome. encoding/json normally accepts duplicate
// object keys and replaces invalid UTF-8; either behavior can erase decisive
// failure evidence. Unknown fields remain permitted for protocol evolution.
// consume has already checked that raw is a single JSON object and retains
// the exact original frame before this semantic validation.
func validateEventJSON(raw []byte) error {
	if !utf8.Valid(raw) {
		return errors.New("event must be valid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return errors.New("event JSON nesting exceeds 64 levels")
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				keyToken, err := d.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("event object key must be a string")
				}
				if seen[key] {
					return fmt.Errorf("duplicate event object key %q", key)
				}
				seen[key] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			_, err = d.Token()
		case json.Delim('['):
			for d.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			_, err = d.Token()
		}
		return err
	}
	return value(0)
}
