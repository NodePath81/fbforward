package flow

import (
	"crypto/rand"
	"encoding/base64"
)

// ID is the opaque identifier shared by all projections of one Flow.
type ID string

const idEntropyBytes = 16

// NewID returns a cryptographically random, URL-safe Flow identifier.
func NewID() (ID, error) {
	var raw [idEntropyBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return ID("f_" + base64.RawURLEncoding.EncodeToString(raw[:])), nil
}

func (id ID) String() string {
	return string(id)
}
