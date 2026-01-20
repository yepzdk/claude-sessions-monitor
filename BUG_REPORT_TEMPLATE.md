# Bug Report for anthropics/claude-code

Submit at: https://github.com/anthropics/claude-code/issues/new?template=bug_report.yml

---

## Preflight Checklist
- [x] I have searched the existing issues to make sure this bug has not been reported yet
- [x] I am reporting a single bug in this issue
- [x] I am using the latest version of Claude Code

---

## What's Wrong? (actual)

When using Claude Code features within Claude Desktop on macOS, the spawned `claude` CLI process is not terminated when the Desktop app is closed. The orphaned process persists indefinitely (observed running for 3+ days) and consumes system resources.

Example:
```
$ ps ax -o pid,comm,lstart | grep claude
62019 claude           Fri Jan 16 13:35:22 2026   # 3 days old, still running after Desktop closed
45595 claude           Mon Jan 19 07:42:22 2026   # Current terminal session
```

---

## What Should Happen? (expected)

When Claude Desktop is closed (via Cmd+Q or Dock → Quit), all spawned child processes including `claude` CLI processes should be terminated gracefully.

---

## Error Messages/Logs (error_output)

No error messages. The process simply continues running silently after Desktop closes.

```shell
# Orphaned process detected 3 days after Desktop was closed:
$ ps ax -o pid,comm,lstart | grep claude
62019 claude           Fri Jan 16 13:35:22 2026

# Process must be manually killed:
$ kill 62019
```

---

## Steps to Reproduce (reproduction)

1. Launch Claude Desktop on macOS
2. Start a Claude Code session within Desktop (open any project folder)
3. Perform some Claude Code actions (file edits, bash commands, etc.)
4. Close Claude Desktop using Cmd+Q or Dock right-click → Quit
5. Verify the `claude` process is still running:
   ```bash
   ps ax -o pid,comm,lstart | grep claude
   ```
6. The process from step 2-3 will still be running with the old start time

---

## Claude Model (model)

claude-opus-4-5-20251101 (but likely affects all models)

---

## Is this a regression? (regression)

Unknown - first time noticing this behavior

---

## Last Working Version (working_version)

N/A

---

## Claude Code Version (version)

```
[Run: claude --version and paste output here]
```

---

## Platform (platform)

Anthropic API

---

## Operating System (os)

macOS (arm64 / Apple Silicon)

---

## Terminal/Shell (terminal)

N/A - occurs when using Claude Code through Desktop app, not terminal

---

## Additional Information (additional)

### Related Issues
- #14951 - Claude Desktop macOS zombie state (closed - about app lifecycle, not child processes)
- #1935 - MCP servers not terminated (open - similar pattern but for MCP processes)
- #15423 - Windows orphaned renderer processes (related pattern on Windows)

### Root Cause Hypothesis
Claude Desktop spawns `claude` CLI processes to handle Claude Code functionality. These child processes are not being tracked/terminated during the app shutdown sequence. Unlike MCP server orphaning (#1935), these are the main Claude Code execution processes.

### Workaround
Manually identify and kill orphaned processes:
```bash
# Find old claude processes
ps ax -o pid,comm,lstart | grep claude

# Kill orphaned process
kill <PID>
```

### Impact
- Memory/resource consumption from zombie processes
- Processes accumulate over multiple sessions
- Confuses session monitoring tools (processes appear "active")
