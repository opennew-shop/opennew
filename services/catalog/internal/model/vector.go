package model

import (
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
)

// Vector represents a pgvector vector type as a Go []float32 slice.
// It implements sql.Scanner and driver.Valuer for seamless database round-tripping.
//
// pgvector stores vectors as a string representation: [0.1,0.2,0.3,...]
// This type handles serialization/deserialization transparently.
type Vector []float32

// Scan implements sql.Scanner. It reads the pgvector string representation
// from the database and parses it into a Go []float32.
func (v *Vector) Scan(src interface{}) error {
	if src == nil {
		*v = nil
		return nil
	}

	var str string
	switch s := src.(type) {
	case string:
		str = s
	case []byte:
		str = string(s)
	default:
		return fmt.Errorf("vector: cannot scan type %T into Vector", src)
	}

	str = strings.TrimSpace(str)
	if str == "" || str == "[]" || str == "[ ]" {
		*v = nil
		return nil
	}

	// Expect format: [0.1,0.2,0.3,...]
	if !strings.HasPrefix(str, "[") || !strings.HasSuffix(str, "]") {
		return fmt.Errorf("vector: invalid pgvector format: %s", str)
	}

	inner := str[1 : len(str)-1]
	parts := strings.Split(inner, ",")

	vec := make(Vector, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		f, err := strconv.ParseFloat(part, 32)
		if err != nil {
			return fmt.Errorf("vector: cannot parse float %q: %w", part, err)
		}
		vec = append(vec, float32(f))
	}

	*v = vec
	return nil
}

// Value implements driver.Valuer. It converts the Go []float32 to the
// pgvector string representation for storage.
func (v Vector) Value() (driver.Value, error) {
	if v == nil {
		return nil, nil
	}

	if len(v) == 0 {
		return "[]", nil
	}

	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = strconv.FormatFloat(float64(f), 'f', -1, 32)
	}

	return fmt.Sprintf("[%s]", strings.Join(parts, ",")), nil
}

// Dimension returns the number of elements in the vector.
func (v Vector) Dimension() int {
	return len(v)
}

// String returns a human-readable representation of the vector.
func (v Vector) String() string {
	s, _ := v.Value()
	if s == nil {
		return "<nil>"
	}
	return s.(string)
}
