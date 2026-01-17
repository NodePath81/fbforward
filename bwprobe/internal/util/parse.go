package util

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ParseBandwidth parses a bandwidth string (e.g., "100Mbps", "1Gbps") and returns bits per second
func ParseBandwidth(input string) (float64, error) {
	s := strings.TrimSpace(strings.ToLower(input))
	s = strings.ReplaceAll(s, " ", "")
	if s == "" {
		return 0, errors.New("bandwidth is empty")
	}

	re := regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)([a-z/]+)?$`)
	match := re.FindStringSubmatch(s)
	if match == nil {
		return 0, fmt.Errorf("invalid bandwidth %q", input)
	}

	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid bandwidth %q", input)
	}

	unit := match[2]
	if unit == "" || unit == "bps" {
		return value, nil
	}

	switch unit {
	case "kbps":
		return value * 1e3, nil
	case "mbps":
		return value * 1e6, nil
	case "gbps":
		return value * 1e9, nil
	case "tbps":
		return value * 1e12, nil
	case "k":
		return value * 1e3, nil
	case "m":
		return value * 1e6, nil
	case "g":
		return value * 1e9, nil
	case "t":
		return value * 1e12, nil
	case "b/s":
		return value, nil
	case "kb/s":
		return value * 1e3 * 8, nil
	case "mb/s":
		return value * 1e6 * 8, nil
	case "gb/s":
		return value * 1e9 * 8, nil
	case "tb/s":
		return value * 1e12 * 8, nil
	default:
		return 0, fmt.Errorf("unknown bandwidth unit %q", unit)
	}
}

// ParseBytes parses a size string (e.g., "200MB", "1500KB") and returns bytes.
func ParseBytes(input string) (int64, error) {
	s := strings.TrimSpace(strings.ToLower(input))
	s = strings.ReplaceAll(s, " ", "")
	if s == "" {
		return 0, errors.New("bytes value is empty")
	}

	re := regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)([a-z]+)?$`)
	match := re.FindStringSubmatch(s)
	if match == nil {
		return 0, fmt.Errorf("invalid bytes value %q", input)
	}

	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid bytes value %q", input)
	}
	if value < 0 {
		return 0, errors.New("bytes value must be >= 0")
	}

	unit := match[2]
	if unit == "" || unit == "b" {
		return int64(math.Round(value)), nil
	}

	switch unit {
	case "kb":
		return int64(math.Round(value * 1e3)), nil
	case "mb":
		return int64(math.Round(value * 1e6)), nil
	case "gb":
		return int64(math.Round(value * 1e9)), nil
	case "tb":
		return int64(math.Round(value * 1e12)), nil
	default:
		return 0, fmt.Errorf("unknown bytes unit %q", unit)
	}
}

// DurationFromSeconds converts seconds (float) to time.Duration
func DurationFromSeconds(sec float64) time.Duration {
	if sec <= 0 {
		return 0
	}
	return time.Duration(sec * float64(time.Second))
}

// MinDuration returns the minimum of two durations
func MinDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
