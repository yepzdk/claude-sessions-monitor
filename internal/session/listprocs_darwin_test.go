//go:build darwin

package session

import (
	"encoding/binary"
	"slices"
	"testing"

	"golang.org/x/sys/unix"
)

// The orphan test is ppid == 1. Read from Proc.P_oppid, which the kernel fills
// in only under a debugger and leaves at zero otherwise, it never fires, and no
// macOS session is ever badged a ghost.
func TestKinfoToProcInfoReadsTheRealParentPID(t *testing.T) {
	var kp unix.KinfoProc
	kp.Proc.P_pid = 1868
	kp.Eproc.Ppid = 1
	kp.Proc.P_oppid = 4321
	copy(kp.Proc.P_comm[:], "claude")

	got := kinfoToProcInfo(&kp)
	want := procInfo{pid: 1868, ppid: 1, comm: "claude"}
	if got != want {
		t.Errorf("kinfoToProcInfo = %+v, want %+v", got, want)
	}
}

// kern.procargs2 hands back one buffer holding the argument count, the resolved
// executable path, alignment padding, the arguments and then the environment,
// with nothing but the count to say where the arguments stop. Walking it wrong
// silently yields a plausible argv -- the exec path as argv[0], or an
// environment variable appended as a trailing argument -- and classifyProcess
// then decides from it.
func TestParseProcArgs2WalksTheBufferTheKernelActuallyReturns(t *testing.T) {
	// A claude launched through a symlink, so the resolved exec path and
	// argv[0] differ. Taking the exec path as argv[0] is the mistake this
	// pins: it would classify by the real binary's name rather than by the
	// name the user invoked.
	buf := procArgs2Buffer(2,
		"/opt/homebrew/Cellar/claude/0.7.0/bin/claude-bin",
		[]string{"/Users/dev/.local/bin/claude", "--resume"},
		[]string{"PATH=/usr/bin", "HOME=/Users/dev"},
	)

	argv, err := parseProcArgs2(buf)
	if err != nil {
		t.Fatalf("parseProcArgs2: %v", err)
	}
	want := []string{"/Users/dev/.local/bin/claude", "--resume"}
	if !slices.Equal(argv, want) {
		t.Errorf("parseProcArgs2 = %q, want %q", argv, want)
	}
	if got := classifyProcess(argv); got != HarnessClaude {
		t.Errorf("classifyProcess(%q) = %q, want %q", argv, got, HarnessClaude)
	}
}

// A path holding a space is one argument in the buffer, and stays one here.
// This is the case that a printed command line cannot represent.
func TestParseProcArgs2KeepsAPathHoldingASpaceWhole(t *testing.T) {
	buf := procArgs2Buffer(1, "/Volumes/My Disk/bin/claude",
		[]string{"/Volumes/My Disk/bin/claude"}, nil)

	argv, err := parseProcArgs2(buf)
	if err != nil {
		t.Fatalf("parseProcArgs2: %v", err)
	}
	if len(argv) != 1 || argv[0] != "/Volumes/My Disk/bin/claude" {
		t.Fatalf("parseProcArgs2 = %q, want one element holding the whole path", argv)
	}
}

// A short or inconsistent buffer must be reported. Returning a partial argv
// would let the kill guard classify from arguments that were never read.
func TestParseProcArgs2RejectsABufferItCannotWalk(t *testing.T) {
	tests := []struct {
		name string
		buf  []byte
	}{
		{"too short to hold a count", []byte{1, 0}},
		{"no arguments at all", procArgs2Buffer(0, "/bin/x", nil, nil)},
		{"count larger than the buffer", append(binary.NativeEndian.AppendUint32(nil, 99), "/bin/x\x00arg\x00"...)},
		{"ends before the last argument", procArgs2Buffer(3, "/bin/x", []string{"a", "b"}, nil)},
		{"no terminated executable path", binary.NativeEndian.AppendUint32(nil, 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if argv, err := parseProcArgs2(tt.buf); err == nil {
				t.Errorf("parsed %q out of a buffer that cannot be walked", argv)
			}
		})
	}
}

// procArgs2Buffer builds a buffer in the layout kern.procargs2 returns. argc is
// passed separately from the arguments so a test can claim a count the buffer
// does not deliver.
func procArgs2Buffer(argc uint32, execPath string, argv, env []string) []byte {
	buf := binary.NativeEndian.AppendUint32(nil, argc)
	buf = append(buf, execPath...)
	// The kernel terminates the exec path and then pads to an alignment
	// boundary with more NULs, so the walk cannot assume exactly one.
	buf = append(buf, 0, 0, 0)
	for _, arg := range argv {
		buf = append(buf, arg...)
		buf = append(buf, 0)
	}
	for _, e := range env {
		buf = append(buf, e...)
		buf = append(buf, 0)
	}
	return buf
}
