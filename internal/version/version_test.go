package version_test

import (
	"testing"

	"github.com/silentfirewall/squawk/internal/version"
)

func TestVersionDefault(t *testing.T) {
	if version.Version == "" {
		t.Fatal("version must not be empty")
	}
}
