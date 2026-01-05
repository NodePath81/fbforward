package main

import (
	"fmt"
	"strings"
)

// parseBandwidth parses a human-readable bandwidth string to bits/sec.
// Supports formats: "100", "100k", "100m", "100g" (case insensitive).
// Units: k=1000, m=1000000, g=1000000000 (SI units, not binary).
func parseBandwidth(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	s = strings.ToLower(s)

	multiplier := uint64(1)
	numStr := s

	if len(s) > 0 {
		lastChar := s[len(s)-1]
		switch lastChar {
		case 'k':
			multiplier = 1_000
			numStr = s[:len(s)-1]
		case 'm':
			multiplier = 1_000_000
			numStr = s[:len(s)-1]
		case 'g':
			multiplier = 1_000_000_000
			numStr = s[:len(s)-1]
		}
	}

	numStr = strings.TrimSpace(numStr)
	if numStr == "" {
		return 0, fmt.Errorf("invalid bandwidth value: %q", s)
	}

	var value float64
	if _, err := fmt.Sscanf(numStr, "%f", &value); err != nil {
		return 0, fmt.Errorf("invalid bandwidth value: %q", s)
	}

	if value < 0 {
		return 0, fmt.Errorf("bandwidth cannot be negative: %q", s)
	}

	return uint64(value * float64(multiplier)), nil
}

// parseSize parses a human-readable size string to bytes.
// Supports formats: "100", "16k", "32m" (case insensitive).
// Units: k=1024, m=1048576 (binary units for buffer sizes).
func parseSize(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	s = strings.ToLower(s)

	multiplier := uint64(1)
	numStr := s

	if len(s) > 0 {
		lastChar := s[len(s)-1]
		switch lastChar {
		case 'k':
			multiplier = 1024
			numStr = s[:len(s)-1]
		case 'm':
			multiplier = 1024 * 1024
			numStr = s[:len(s)-1]
		}
	}

	numStr = strings.TrimSpace(numStr)
	if numStr == "" {
		return 0, fmt.Errorf("invalid size value: %q", s)
	}

	var value float64
	if _, err := fmt.Sscanf(numStr, "%f", &value); err != nil {
		return 0, fmt.Errorf("invalid size value: %q", s)
	}

	if value < 0 {
		return 0, fmt.Errorf("size cannot be negative: %q", s)
	}

	return uint32(value * float64(multiplier)), nil
}
