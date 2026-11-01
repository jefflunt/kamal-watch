package monitor

import (
	"testing"
)

func TestParseBytes(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
	}{
		{"", 0},
		{"-", 0},
		{"100B", 100},
		{"500KiB", 500 * 1024},
		{"500kB", 500 * 1024},
		{"160.8MiB", 168611020},
		{"1.5GiB", 1610612736},
	}

	for _, tt := range tests {
		got := parseBytes(tt.input)
		// Allow small float rounding delta
		diff := got - tt.expected
		if diff < -1024 || diff > 1024 {
			t.Errorf("parseBytes(%q) = %d; expected %d", tt.input, got, tt.expected)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "-"},
		{-1, "-"},
		{500, "500B"},
		{1024 * 500, "500.0KiB"},
		{1024 * 1024 * 160, "160.0MiB"},
		{int64(1024 * 1024 * 1024 * 2.5), "2.50GiB"},
	}

	for _, tt := range tests {
		got := formatBytes(tt.input)
		if got != tt.expected {
			t.Errorf("formatBytes(%d) = %q; expected %q", tt.input, got, tt.expected)
		}
	}
}
