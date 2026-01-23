//go:build windows

package cli

import (
	"os"
)

// Windows doesn't have SIGUSR1, use a dummy signal that won't be used
var signalUSR1 = os.Interrupt
