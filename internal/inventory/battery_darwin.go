//go:build darwin

package inventory

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// hasBattery asks pmset for the internal battery every MacBook has. The model
// id doesn't say: an M2 MacBook laptop is "Mac14,15".
func hasBattery() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pmset", "-g", "batt").Output()
	return err == nil && strings.Contains(string(out), "InternalBattery")
}
