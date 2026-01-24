package account

import (
	"bytes"
	"encoding/json"
)

// NullableString unmarshals JSON null as an empty string (""), matching Node config files
// where fields like invalidReason and dbPath may be null.
type NullableString string

func (s *NullableString) UnmarshalJSON(b []byte) error {
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		*s = ""
		return nil
	}
	var v string
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*s = NullableString(v)
	return nil
}

func (s NullableString) MarshalJSON() ([]byte, error) {
	// Node uses null for "empty" nullable strings in config.
	if s == "" {
		return []byte("null"), nil
	}
	return json.Marshal(string(s))
}
