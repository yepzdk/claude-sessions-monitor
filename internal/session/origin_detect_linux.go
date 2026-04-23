//go:build linux

package session

import (
	"bytes"
	"fmt"
	"os"
	"strings"
)

// readProcessEnv returns the environment of a running process on Linux via
// /proc/<pid>/environ. Only readable when csm runs as the same UID as the
// target process; returns an empty map on permission errors.
func readProcessEnv(pid int) map[string]string {
	env := make(map[string]string)
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return env
	}
	for _, entry := range bytes.Split(data, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		eq := bytes.IndexByte(entry, '=')
		if eq <= 0 {
			continue
		}
		env[string(entry[:eq])] = string(entry[eq+1:])
	}
	return env
}

// parentChain walks ancestors using /proc/<pid>/status for ppid and
// /proc/<pid>/exe for the executable path (falling back to /proc/<pid>/comm
// when exe is not readable).
func parentChain(pid int) []ProcessInfo {
	var chain []ProcessInfo
	current := pid
	for hops := 0; hops < 10 && current > 1; hops++ {
		ppid, ok := readPPid(current)
		if !ok {
			return chain
		}
		comm := readComm(current)
		exe := readExe(current)
		chain = append(chain, ProcessInfo{PID: current, Comm: comm, Exe: exe})
		if ppid <= 1 {
			return chain
		}
		current = ppid
	}
	return chain
}

func readPPid(pid int) (int, bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "PPid:") {
			var ppid int
			if _, err := fmt.Sscanf(line, "PPid:\t%d", &ppid); err == nil {
				return ppid, true
			}
		}
	}
	return 0, false
}

func readComm(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func readExe(pid int) string {
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return ""
	}
	return exe
}
