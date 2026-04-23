//go:build darwin

package session

import (
	"fmt"
	"os/exec"
	"strings"
)

// readProcessEnv returns the environment of a running process on macOS.
// Uses `ps -E -ww -p <pid> -o command=` to get KEY=VALUE tokens; `-ww`
// disables truncation of the output line.
func readProcessEnv(pid int) map[string]string {
	env := make(map[string]string)
	out, err := exec.Command("ps", "-E", "-ww", "-p", fmt.Sprintf("%d", pid), "-o", "command=").Output()
	if err != nil {
		return env
	}

	// The first token is the command path; everything after is KEY=VALUE
	// separated by spaces. Values containing spaces would break this, but
	// process env values almost never do in practice.
	for _, tok := range strings.Fields(string(out)) {
		eq := strings.IndexByte(tok, '=')
		if eq <= 0 {
			continue
		}
		key := tok[:eq]
		// Key must look like a shell identifier.
		if !isEnvKey(key) {
			continue
		}
		env[key] = tok[eq+1:]
	}
	return env
}

// parentChain walks ancestors starting from pid upward until pid 1 or 10 hops.
// Each step uses `ps -p <pid> -o ppid=,comm=`; on macOS `comm` for GUI-launched
// apps typically returns the full executable path inside the bundle.
func parentChain(pid int) []ProcessInfo {
	var chain []ProcessInfo
	current := pid
	for hops := 0; hops < 10 && current > 1; hops++ {
		out, err := exec.Command("ps", "-p", fmt.Sprintf("%d", current), "-o", "ppid=,comm=").Output()
		if err != nil {
			return chain
		}
		line := strings.TrimSpace(string(out))
		if line == "" {
			return chain
		}
		// ppid then comm; comm may contain spaces so split on first field only.
		fields := strings.SplitN(line, " ", 2)
		if len(fields) < 2 {
			return chain
		}
		var ppid int
		if _, err := fmt.Sscanf(fields[0], "%d", &ppid); err != nil {
			return chain
		}
		comm := strings.TrimSpace(fields[1])
		chain = append(chain, ProcessInfo{PID: current, Comm: comm, Exe: comm})
		if ppid <= 1 {
			return chain
		}
		current = ppid
	}
	return chain
}

// isEnvKey reports whether s looks like a POSIX env var name.
func isEnvKey(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		isAlpha := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_'
		isDigit := r >= '0' && r <= '9'
		if i == 0 && !isAlpha {
			return false
		}
		if !isAlpha && !isDigit {
			return false
		}
	}
	return true
}
