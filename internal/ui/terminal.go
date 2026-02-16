package ui

import (
	"os"
	"syscall"
	"unsafe"
)

const defaultTerminalWidth = 120

// getTerminalWidth returns the current terminal width in columns.
// Falls back to defaultTerminalWidth if detection fails.
func getTerminalWidth() int {
	var ws struct {
		Row    uint16
		Col    uint16
		Xpixel uint16
		Ypixel uint16
	}

	fd := os.Stdout.Fd()
	_, _, err := syscall.Syscall(
		syscall.SYS_IOCTL,
		fd,
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(&ws)),
	)
	if err != 0 || ws.Col == 0 {
		return defaultTerminalWidth
	}
	return int(ws.Col)
}
