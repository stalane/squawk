//go:build linux

package service_test

import (
	"strings"
	"testing"

	"github.com/stalane/squawk/internal/service"
)

func TestLinuxUnit(t *testing.T) {
	unit := service.LinuxUnit("/home/u/.local/bin/squawk", "/home/u/.config/squawk/config.yaml")
	if !strings.Contains(unit, "ExecStart=/home/u/.local/bin/squawk serve --config /home/u/.config/squawk/config.yaml") {
		t.Fatalf("unit: %s", unit)
	}
	if !strings.Contains(unit, "Restart=on-failure") {
		t.Fatal("missing restart policy")
	}
	if !strings.Contains(unit, "WantedBy=default.target") {
		t.Fatal("missing install target")
	}
}
