package config

import (
	"fmt"
	"strings"
)

// ParseBandwidth parses a human-readable bandwidth string to bits/sec.
// Supports formats: "100k", "100m", "100g" (case insensitive).
// Bare numbers are rejected except for zero ("0" or "0.0").
// Units: k=1000, m=1000000, g=1000000000 (SI units, not binary).
func ParseBandwidth(s string) (uint64, error) {
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
		default:
			if s == "0" || s == "0.0" {
				return 0, nil
			}
			return 0, fmt.Errorf("bandwidth must include unit suffix (k/m/g): %q", s)
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

// ParseSize parses a human-readable size string to bytes.
// Supports formats: "100", "500kb", "1mb" (case insensitive).
// Units: kb=1000, mb=1000000 (decimal bytes).
func ParseSize(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	s = strings.ToLower(s)

	multiplier := uint64(1)
	numStr := s

	switch {
	case strings.HasSuffix(s, "kb"):
		multiplier = 1_000
		numStr = s[:len(s)-2]
	case strings.HasSuffix(s, "mb"):
		multiplier = 1_000_000
		numStr = s[:len(s)-2]
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
