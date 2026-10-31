//go:build !windows

package tools

import (
	"context"
	"errors"
	"time"
)

// Administrator elevation is a Windows feature. Elsewhere the tool is not
// registered, so the model never sees it.
func elevationSupported() bool { return false }

func isElevated() bool { return false }

func runPowerShell(context.Context, string, time.Duration, bool) (int, bool, error) {
	return 1, false, errors.New("run_elevated is only available on Windows")
}
