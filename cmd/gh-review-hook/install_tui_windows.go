//go:build windows

package main

import "fmt"

func selectTarget(_ []installTarget) (int, bool, error) {
	return 0, false, fmt.Errorf("interactive installation is not supported on Windows; edit the settings file manually")
}
