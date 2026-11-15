package domain

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Cursor is the keyset position for GET /incidents (Plan_Detailed §8.4).
// sort=severity uses S, M, I; sort=time uses T, I.
// It lives in domain (not httpapi) so both httpapi (validation) and
// store/postgres (SQL) can use it without breaking import rules R3/R4.
type Cursor struct {
	S *int16     `json:"s,omitempty"` // severity
	M *float64   `json:"m,omitempty"` // magnitude (coalesce(magnitude,-1))
	T *time.Time `json:"t,omitempty"` // start time
	I int64      `json:"i"`           // id (tie-breaker)
}

// EncodeCursor returns the opaque, URL-safe cursor string.
func EncodeCursor(c Cursor) string {
	b, _ := json.Marshal(c) // cannot fail: plain struct
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor parses a cursor produced by EncodeCursor. Standard and URL-safe
// base64, with or without padding, are accepted. Errors wrap ErrInvalid.
func DecodeCursor(s string) (Cursor, error) {
	var c Cursor
	raw := strings.TrimRight(s, "=")
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		b, err = base64.RawStdEncoding.DecodeString(raw)
	}
	if err != nil {
		return c, fmt.Errorf("cursor is not valid base64: %w", ErrInvalid)
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("cursor is not valid JSON: %w", ErrInvalid)
	}
	if c.I <= 0 {
		return c, fmt.Errorf("cursor has no id: %w", ErrInvalid)
	}
	return c, nil
}
