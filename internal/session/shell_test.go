package session

import (
	"slices"
	"strings"
	"testing"
)

// commands renders the segments as "name arg arg" lines, which is what the
// rules actually see and the only thing worth asserting.
func commands(cmd string) []string {
	var out []string
	for _, s := range splitCommand(cmd) {
		name, args := s.Command()
		if name == "" {
			continue
		}
		line := name
		if len(args) > 0 {
			line += " " + strings.Join(args, " ")
		}
		if s.Pipe {
			line += " |"
		}
		out = append(out, line)
	}
	return out
}

func assertCommands(t *testing.T, cmd string, want ...string) {
	t.Helper()
	if got := commands(cmd); !slices.Equal(got, want) {
		t.Fatalf("%q\n got %q\nwant %q", cmd, got, want)
	}
}

func TestSplitCommandSeparators(t *testing.T) {
	assertCommands(t, "ls -la", "ls -la")
	assertCommands(t, "cd /tmp && rm -rf x", "cd /tmp", "rm -rf x")
	assertCommands(t, "a; b || c", "a", "b", "c")
	assertCommands(t, "cat f | grep x", "cat f |", "grep x")
	assertCommands(t, "a &\nb", "a", "b")
	// A command substitution runs its contents, so they are their own segment.
	assertCommands(t, `echo $(rm -rf /x)`, "echo", "rm -rf /x")
	// Leading environment assignments are not the command.
	assertCommands(t, "FOO=1 BAR=2 make build", "make build")
	// The command is matched by basename, so a path cannot hide it and a
	// lookalike cannot impersonate it.
	assertCommands(t, "/usr/local/bin/rm -rf /x", "rm -rf /x")
	assertCommands(t, "./scripts/sudo-helper up", "sudo-helper up")
}

// Quoting is the difference between running a command and talking about one.
// Agents narrate constantly -- `echo "=== would git clean remove this? ==="` is
// verbatim from a real session -- and a reader that ignores quotes flags it.
func TestSplitCommandRespectsQuoting(t *testing.T) {
	assertCommands(t, `echo "rm -rf /"`, "echo rm -rf /")
	assertCommands(t, `echo 'sudo shutdown'`, "echo sudo shutdown")
	assertCommands(t, `echo "=== would git clean -dff remove the auth file? ==="`,
		"echo === would git clean -dff remove the auth file? ===")
	// A quoted argument stays one word even when it contains separators.
	assertCommands(t, `grep "a|b;c" file`, "grep a|b;c file")
	assertCommands(t, `echo "he said \"hi\""`, `echo he said "hi"`)
}

// A heredoc body is data the command is fed, not commands. Agents write files
// by heredoc constantly, and those files are full of shell examples -- this is
// the single largest source of false positives in a real corpus.
func TestSplitCommandDropsHeredocBodies(t *testing.T) {
	assertCommands(t, "cat > f <<'EOF'\nrm -rf /\nsudo reboot\nEOF\necho done",
		"cat", "echo done")
	// <<- strips leading tabs from the terminator.
	assertCommands(t, "cat <<-END\n\tsudo rm -rf /\n\tEND\nls", "cat", "ls")
	// Two heredocs opened on one line consume two bodies.
	assertCommands(t, "cmd <<A <<B\nsudo x\nA\nsudo y\nB\nls", "cmd", "ls")
	// An unterminated heredoc swallows the rest rather than leaking its body.
	assertCommands(t, "cat <<'EOF'\nsudo rm -rf /", "cat")
	// A here-string is not a heredoc: the command after it still runs.
	assertCommands(t, "grep x <<< \"$data\"\nls", "grep x", "ls")
}

// A redirection target is a filename. Left as a word it becomes a segment, and
// `echo x > sudo` reads as running sudo.
func TestSplitCommandDropsRedirectionTargets(t *testing.T) {
	assertCommands(t, "echo x > sudo", "echo x")
	assertCommands(t, "cmd 2>&1 | tail -5", "cmd |", "tail -5")
	assertCommands(t, "gh api repos/x/y > /tmp/out.json", "gh api repos/x/y")
	assertCommands(t, "sort < in.txt > out.txt", "sort")
}

// Verbatim commands from a real corpus that earlier iterations of this feature
// flagged and should not. Each one is a distinct way of being wrong.
func TestSplitCommandOnRealFalsePositives(t *testing.T) {
	cases := []struct {
		name    string
		cmd     string
		absent  string // a command that must not appear in the segments
		present string // one that must
	}{
		{
			name:    "commit message heredoc mentioning a push",
			cmd:     "git add -A && git commit -q -F - <<'EOF'\ndocs: add guide\n\ngit push origin main is documented here\nEOF",
			absent:  "git push origin main",
			present: "git add -A",
		},
		{
			name:    "skill file written by heredoc containing real commands",
			cmd:     "mkdir -p .claude/skills/release && cat > .claude/skills/release/SKILL.md <<'SKILLEOF'\ngit push origin main\nrm -rf ~/old\nSKILLEOF",
			absent:  "git push origin main",
			present: "mkdir -p .claude/skills/release",
		},
		{
			name:    "narration quoting a command it is asking about",
			cmd:     `echo "=== reconfirm: git clean would remove the auth file ==="; git clean -dffn`,
			absent:  "git clean -dff",
			present: "git clean -dffn",
		},
		{
			name:    "docker running a shell inside a container",
			cmd:     `docker run --rm --entrypoint sh img:latest -c 'cat /usr/local/bin/docker-entrypoint.sh'`,
			absent:  "sh",
			present: "docker run --rm --entrypoint sh img:latest -c cat /usr/local/bin/docker-entrypoint.sh",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := commands(tc.cmd)
			for _, g := range got {
				if strings.TrimSuffix(g, " |") == tc.absent {
					t.Fatalf("segment %q should not be a command here; got %q", tc.absent, got)
				}
			}
			for _, g := range got {
				if strings.TrimSuffix(g, " |") == tc.present {
					return
				}
			}
			t.Fatalf("expected segment %q, got %q", tc.present, got)
		})
	}
}
