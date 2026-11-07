package service

import "errors"

var ErrUnsupported = errors.New("autostart is not supported on this platform yet")

func Install(exePath, configPath string) error { return install(exePath, configPath) }
func Uninstall() error                         { return uninstall() }
func Status() (bool, string, error)            { return status() }
