//go:build darwin

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const plistName = "com.silentfirewall.squawk.plist"

func install(exePath, configPath string) error {
	path, err := plistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(LaunchdPlist(exePath, configPath)), 0o644); err != nil {
		return err
	}
	if out, err := exec.Command("launchctl", "load", "-w", path).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl load: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func uninstall() error {
	path, err := plistPath()
	if err != nil {
		return err
	}
	_, _ = exec.Command("launchctl", "unload", "-w", path).CombinedOutput()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func status() (bool, string, error) {
	path, err := plistPath()
	if err != nil {
		return false, "", err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, "not installed", nil
	}
	out, _ := exec.Command("launchctl", "list", "com.silentfirewall.squawk").CombinedOutput()
	return true, strings.TrimSpace(string(out)), nil
}

func LaunchdPlist(exePath, configPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.silentfirewall.squawk</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>serve</string>
    <string>--config</string>
    <string>%s</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict>
</plist>
`, exePath, configPath)
}

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", plistName), nil
}
