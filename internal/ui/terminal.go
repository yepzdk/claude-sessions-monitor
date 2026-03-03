package ui

import (
	"os"
	"syscall"
	"unsafe"
)

const (
	defaultTerminalWidth  = 120
	defaultTerminalHeight = 40
)

// winsize holds the terminal dimensions from TIOCGWINSZ.
type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

// getWinsize queries the terminal for its current dimensions.
func getWinsize() (winsize, bool) {
	var ws winsize
	fd := os.Stdout.Fd()
	_, _, err := syscall.Syscall(
		syscall.SYS_IOCTL,
		fd,
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(&ws)),
	)
	if err != 0 {
		return ws, false
	}
	return ws, true
}

// getTerminalWidth returns the current terminal width in columns.
// Falls back to defaultTerminalWidth if detection fails.
func getTerminalWidth() int {
	ws, ok := getWinsize()
	if !ok || ws.Col == 0 {
		return defaultTerminalWidth
	}
	return int(ws.Col)
}

// getTerminalHeight returns the current terminal height in rows.
// Falls back to defaultTerminalHeight if detection fails.
func getTerminalHeight() int {
	ws, ok := getWinsize()
	if !ok || ws.Row == 0 {
		return defaultTerminalHeight
	}
	return int(ws.Row)
}
