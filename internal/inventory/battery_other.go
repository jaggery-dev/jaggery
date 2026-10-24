//go:build !linux && !darwin

package inventory

// hasBattery is unknown here yet; the Windows service (A5) can ask
// GetSystemPowerStatus. Treated as always on until then.
func hasBattery() bool { return false }
