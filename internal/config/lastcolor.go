package config

import (
	"os"
	"strings"
)

const defaultColor = "ffffff"

// ReadLastColor returns the cached last-applied colour (hex, no '#'), or
// the default white if the cache is missing or empty — mirrors ctl.py's
// read_last_color.
func ReadLastColor(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return defaultColor
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return defaultColor
	}
	return s
}

// WriteLastColor persists the last-applied colour (hex, no '#').
func WriteLastColor(path, hexColor string) error {
	return os.WriteFile(path, []byte(hexColor), 0o644)
}
