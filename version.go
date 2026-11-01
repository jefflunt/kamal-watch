package kamalwatch

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var rawVersion string

// Version returns the semantic version string embedded from the root VERSION file.
func Version() string {
	v := strings.TrimSpace(rawVersion)
	if v == "" {
		return "dev"
	}
	return strings.TrimPrefix(v, "v")
}
