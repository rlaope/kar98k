//go:build !windows

package cli

import (
	"syscall"
)

var signalUSR1 = syscall.SIGUSR1
