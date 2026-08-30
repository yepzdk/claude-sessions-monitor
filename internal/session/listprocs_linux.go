//go:build linux

package session

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// procRoot is where procfs is mounted. A var so a test can point the scan at a
// fixture tree instead of the machine's own process table.
var procRoot = "/proc"

// listProcessesNative reads the process table out of procfs.
//
// A pid that exits between the listing and the read is skipped. The table moves
// while it is being read, and one departed process is not a failed scan. A
// procfs that cannot be listed at all is a failed scan and is reported. A table
// that lists but yields nothing is rejected by the caller.
func listProcessesNative() ([]procInfo, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", procRoot, err)
	}

	procs := make([]procInfo, 0, len(entries))
	for _, e := range entries {
		// procfs also holds named entries such as self, meminfo and net.
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		// A pid that is gone exited between the listing and the read, which
		// is normal. A table that comes back empty is rejected by the caller.
		data, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "stat"))
		if err != nil {
			continue
		}
		comm, ppid, err := parseProcStat(data)
		if err != nil {
			continue
		}
		procs = append(procs, procInfo{pid: pid, ppid: ppid, comm: comm})
	}
	return procs, nil
}

// processArgv returns the full argument vector of one pid.
//
// /proc/<pid>/cmdline holds argv as the kernel stored it: NUL-separated, one
// element per argument. Nothing re-splits it on spaces, so an agent installed
// under a path holding a space stays a single element and classifyProcess sees
// the basename it expects.
//
// The file is empty for a kernel thread, and for a process that has exited but
// whose entry has not been reaped. Both come back as an empty argv, which the
// caller reads as "no live process to attribute".
func processArgv(pid int) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil, err
	}
	// The kernel terminates every element, the last one included, so trimming
	// first is what keeps Split from returning a trailing empty element. An
	// empty element *between* two NULs is a real empty argument and survives.
	trimmed := strings.Trim(string(data), "\x00")
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\x00"), nil
}

// parseProcStat pulls the command name and the parent pid out of one
// /proc/<pid>/stat line.
//
// The name is the second field and sits in parentheses. The kernel does not
// escape it, so a process called "foo) bar" holds both a space and a close
// paren. Splitting the line on whitespace, or cutting at the first ")", shifts
// every field after it and the parent pid then reads as some other number. That
// parent pid is the orphan signal, so a shift marks every session a ghost or
// none of them. Cutting at the last ")" is what keeps the fields lined up.
func parseProcStat(data []byte) (comm string, ppid int, err error) {
	start := bytes.IndexByte(data, '(')
	end := bytes.LastIndexByte(data, ')')
	if start < 0 || end < start {
		return "", 0, errors.New("no command field")
	}
	comm = string(data[start+1 : end])

	// After the name come the state and then the parent pid.
	fields := bytes.Fields(data[end+1:])
	if len(fields) < 2 {
		return "", 0, errors.New("no parent pid after the command field")
	}
	ppid, err = strconv.Atoi(string(fields[1]))
	if err != nil {
		return "", 0, fmt.Errorf("parent pid %q: %w", fields[1], err)
	}
	return comm, ppid, nil
}

// getProcessCwd returns the working directory of a process.
//
// Reading it needs the caller to be the same user as the target process, or
// root. Another user's process gives a permission error, and the caller skips
// that process.
func getProcessCwd(pid int) (string, error) {
	return os.Readlink(filepath.Join(procRoot, strconv.Itoa(pid), "cwd"))
}
