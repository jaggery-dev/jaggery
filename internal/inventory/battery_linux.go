//go:build linux

package inventory

import (
	"os"
	"path/filepath"
	"strings"
)

// hasBattery reports a battery that powers the machine itself. Wireless
// mice and keyboards show up here too, marked scope "Device"; they don't
// make a desktop a laptop.
func hasBattery() bool {
	dirs, _ := filepath.Glob("/sys/class/power_supply/*")
	for _, d := range dirs {
		typ, _ := os.ReadFile(filepath.Join(d, "type"))
		if strings.TrimSpace(string(typ)) != "Battery" {
			continue
		}
		scope, _ := os.ReadFile(filepath.Join(d, "scope"))
		if strings.TrimSpace(string(scope)) != "Device" {
			return true
		}
	}
	return false
}
