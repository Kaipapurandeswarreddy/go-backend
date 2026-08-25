package ids

import (
	"github.com/google/uuid"
)

// New generates a new UUID v4 string. Use for all new Postgres PKs.
// Keeps the same string-typed contract as Mongo's 24-char hex ObjectID —
// callers treat IDs as opaque strings, no parsing needed.
func New() string {
	return uuid.NewString()
}

// IsValid reports whether s is a valid UUID (v4 or any version).
// Replaces primitive.ObjectIDFromHex validation for incoming IDs.
func IsValid(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

// MustParse panics if s is not a valid UUID. Useful for tests/seed.
func MustParse(s string) string {
	if !IsValid(s) {
		panic("ids: invalid UUID: " + s)
	}
	return s
}

// IsZero reports whether s is the zero UUID or empty.
func IsZero(s string) bool {
	if s == "" {
		return true
	}
	u, err := uuid.Parse(s)
	if err != nil {
		return true
	}
	return u == uuid.Nil
}
