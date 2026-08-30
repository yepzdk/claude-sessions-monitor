//go:build darwin

package session

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os/exec"
	"strconv"

	"golang.org/x/sys/unix"
)

// listProcessesNative reads the process table out of the kernel with
// sysctl kern.proc.all. A table that comes back empty is rejected by the
// caller.
func listProcessesNative() ([]procInfo, error) {
	// A scan without lsof is useless: every working directory below would fail
	// to resolve, leaving an empty map that reads downstream as a machine with
	// no Claude session running. Checked once here, where it is a property of
	// the scan, rather than inferred from a run of per-process failures.
	if _, err := exec.LookPath("lsof"); err != nil {
		return nil, fmt.Errorf("lsof is needed to read a process working directory: %w", err)
	}

	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("sysctl kern.proc.all: %w", err)
	}

	out := make([]procInfo, 0, len(procs))
	for i := range procs {
		out = append(out, kinfoToProcInfo(&procs[i]))
	}
	return out, nil
}

// processArgv returns the full argument vector of one pid.
//
// macOS has no procfs. kern.procargs2 is the supported way to read another
// process's argv, and unlike proc_pidinfo it needs no cgo -- which matters
// because the release workflow cross-builds the darwin targets from a Linux
// runner in one job.
func processArgv(pid int) ([]string, error) {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, fmt.Errorf("sysctl kern.procargs2 %d: %w", pid, err)
	}
	return parseProcArgs2(buf)
}

// parseProcArgs2 pulls argv out of a kern.procargs2 buffer.
//
// The layout is a 32-bit argument count, the executable path, NUL padding to
// an alignment boundary, then that many NUL-terminated arguments, and then the
// environment. Two things follow from it. The executable path is skipped
// rather than used as argv[0]: it is the binary the kernel resolved, where
// argv[0] is what the caller actually passed, and the two differ for anything
// launched through a symlink -- which is how Claude Code is installed. And the
// count is what stops the walk, because nothing but the count separates the
// last argument from the first environment variable.
func parseProcArgs2(buf []byte) ([]string, error) {
	if len(buf) < 4 {
		return nil, fmt.Errorf("kern.procargs2 returned %d bytes, too few to hold an argument count", len(buf))
	}
	// Every argument costs at least its terminator, so the buffer's own length
	// bounds the count. The check runs before the width conversion, so a
	// malformed buffer can neither size an allocation nor overflow an int.
	count := binary.NativeEndian.Uint32(buf[:4])
	if count == 0 || uint64(count) > uint64(len(buf)) {
		return nil, fmt.Errorf("kern.procargs2 reports %d arguments in %d bytes", count, len(buf))
	}
	argc := int(count)

	rest := buf[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return nil, errors.New("kern.procargs2 holds no terminated executable path")
	}
	rest = rest[end+1:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}

	argv := make([]string, 0, argc)
	for len(argv) < argc {
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			return nil, fmt.Errorf("kern.procargs2 ended after %d of %d arguments", len(argv), argc)
		}
		argv = append(argv, string(rest[:end]))
		rest = rest[end+1:]
	}
	return argv, nil
}

// kinfoToProcInfo copies the three fields this package reads out of a kinfo_proc.
//
// The parent pid comes from Eproc.Ppid, not from Proc.P_oppid. P_oppid is the
// parent saved while a debugger holds the process and is zero otherwise, so the
// orphan test (ppid == 1) would never fire and no macOS session would ever be
// badged a ghost.
//
// P_comm is the accounting name: the executable's basename, capped at 16 bytes.
// harnessCandidate matches on the suffix, and classifyProcess then decides from
// the full argv.
func kinfoToProcInfo(p *unix.KinfoProc) procInfo {
	return procInfo{
		pid:  int(p.Proc.P_pid),
		ppid: int(p.Eproc.Ppid),
		comm: unix.ByteSliceToString(p.Proc.P_comm[:]),
	}
}

// getProcessCwd returns the working directory of a process.
//
// macOS has no procfs. The supported call is proc_pidinfo in libproc, which
// needs cgo, and the release workflow cross-builds the darwin targets from a
// Linux runner in one job. So this asks lsof.
func getProcessCwd(pid int) (string, error) {
	out, err := exec.Command("lsof", "-p", strconv.Itoa(pid), "-a", "-d", "cwd", "-Fn").Output()
	if err != nil {
		return "", err
	}

	cwd, err := parseLsofCwd(out)
	if err != nil {
		return "", fmt.Errorf("pid %d: %w", pid, err)
	}
	return cwd, nil
}
