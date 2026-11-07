//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const unitName = "squawk.service"

func install(exePath, configPath string) error {
	path, err := unitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(LinuxUnit(exePath, configPath)), 0o644); err != nil {
		return err
	}
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", unitName).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl enable: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func uninstall() error {
	path, err := unitPath()
	if err != nil {
		return err
	}
	_, _ = exec.Command("systemctl", "--user", "disable", "--now", unitName).CombinedOutput()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	_, _ = exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput()
	return nil
}

func status() (bool, string, error) {
	path, err := unitPath()
	if err != nil {
		return false, "", err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, "not installed", nil
	}
	out, _ := exec.Command("systemctl", "--user", "is-active", unitName).Output()
	return true, strings.TrimSpace(string(out)), nil
}

func LinuxUnit(exePath, configPath string) string {
	return fmt.Sprintf(`[Unit]
Description=squawk alert publisher
After=network-online.target

[Service]
ExecStart=%s serve --config %s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, exePath, configPath)
}

func unitPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "systemd", "user", unitName), nil
}
