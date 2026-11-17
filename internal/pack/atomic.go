package pack

import (
	"bytes"
	"encoding/json"
)

// marshalIndent encodes v with two-space indent and a trailing newline.
// Encoding sorts map keys, so the output is stable.
func marshalIndent(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
