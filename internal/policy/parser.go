package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// Parse strictly decodes and validates one policy document.
func Parse(raw []byte) (Document, error) {
	if len(raw) == 0 {
		return Document{}, &ValidationError{Message: "policy document is empty"}
	}
	if len(raw) > MaxPolicyBytes {
		return Document{}, &ValidationError{Message: fmt.Sprintf("policy document exceeds %d bytes", MaxPolicyBytes)}
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	var doc Document
	if err := decoder.Decode(&doc); err != nil {
		return Document{}, &ValidationError{Message: fmt.Sprintf("decode policy document: %v", err)}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Document{}, &ValidationError{Message: "policy document must contain exactly one YAML document"}
		}
		return Document{}, &ValidationError{Message: fmt.Sprintf("decode trailing policy document: %v", err)}
	}
	if err := Validate(&doc); err != nil {
		return Document{}, err
	}
	return doc, nil
}

func ParseFile(path string) (Document, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Document{}, nil, &FileError{Path: path, Err: err}
	}
	if len(raw) > MaxPolicyBytes {
		return Document{}, nil, &ValidationError{Message: fmt.Sprintf("policy file %q exceeds %d bytes", path, MaxPolicyBytes)}
	}
	doc, err := Parse(raw)
	if err != nil {
		return Document{}, nil, err
	}
	return doc, raw, nil
}

type FileError struct {
	Path string
	Err  error
}

func (e *FileError) Error() string { return fmt.Sprintf("read policy file %q: %v", e.Path, e.Err) }
func (e *FileError) Unwrap() error { return e.Err }

func Hash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
