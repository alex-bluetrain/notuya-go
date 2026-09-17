package main

import (
	"os"
	"path/filepath"
	"strings"
)

// defaultColor is what the last-colour cache reports when nothing is stored.
const defaultColor = "ffffff"

// resolveConfigPath applies the precedence: --config flag > NOTUYA_CONFIG env
// > the user's config directory — identical to cmd/notuya.
func resolveConfigPath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv("NOTUYA_CONFIG"); env != "" {
		return env
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "notuya-go", "config.json")
}

// readLastColor returns the cached last-applied colour (hex, no '#'), or the
// default white if the cache is missing or empty.
func readLastColor(path string) string {
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

// writeLastColor persists the last-applied colour (hex, no '#').
func writeLastColor(path, hexColor string) error {
	return os.WriteFile(path, []byte(hexColor), 0o644)
}
