package util

import "fmt"

// FormatBitsPerSecond formats bits per second with appropriate units
func FormatBitsPerSecond(bps float64) string {
	return formatWithUnits(bps, []string{"bps", "Kbps", "Mbps", "Gbps", "Tbps"}, 1000)
}

// FormatBytes formats byte counts with appropriate units
func FormatBytes(bytes float64) string {
	return formatWithUnits(bytes, []string{"B", "KB", "MB", "GB", "TB", "PB"}, 1000)
}

// FormatSeconds formats seconds with appropriate units
func FormatSeconds(sec float64) string {
	if sec < 0 {
		return "0s"
	}
	if sec < 1 {
		ms := sec * 1000
		return fmt.Sprintf("%.2fms", ms)
	}
	if sec < 10 {
		return fmt.Sprintf("%.2fs", sec)
	}
	return fmt.Sprintf("%.1fs", sec)
}

// formatWithUnits is a generic formatter for values with scaling units
func formatWithUnits(value float64, units []string, base float64) string {
	if value < 0 {
		return "0"
	}
	idx := 0
	for value >= base && idx < len(units)-1 {
		value /= base
		idx++
	}
	if value >= 100 {
		return fmt.Sprintf("%.0f %s", value, units[idx])
	}
	if value >= 10 {
		return fmt.Sprintf("%.1f %s", value, units[idx])
	}
	return fmt.Sprintf("%.2f %s", value, units[idx])
}
