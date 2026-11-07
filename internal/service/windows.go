//go:build windows

package service

func install(string, string) error  { return ErrUnsupported }
func uninstall() error              { return ErrUnsupported }
func status() (bool, string, error) { return false, "", ErrUnsupported }
