package session

import "strings"

// OriginCategory is a high-level grouping of where a Claude Code session was launched from.
type OriginCategory string

const (
	OriginUnknown  OriginCategory = ""
	OriginTerminal OriginCategory = "terminal"
	OriginDesktop  OriginCategory = "desktop"
	OriginIDE      OriginCategory = "ide"
)

// Origin describes the environment that spawned a Claude session.
type Origin struct {
	Category OriginCategory `json:"category,omitempty"`
	App      string         `json:"app,omitempty"`     // stable slug: "ghostty", "iterm", "zed", "vscode", "cursor", ...
	Display  string         `json:"display,omitempty"` // pretty name for UI: "Ghostty", "VS Code", ...
}

// IsZero reports whether no origin information is set.
func (o Origin) IsZero() bool {
	return o.Category == OriginUnknown && o.App == "" && o.Display == ""
}

// appCatalog maps the stable app slug to its pretty display name and category.
// Order matters only for documentation; lookup is by slug.
var appCatalog = map[string]struct {
	Display  string
	Category OriginCategory
}{
	// Terminals
	"ghostty":        {"Ghostty", OriginTerminal},
	"iterm":          {"iTerm", OriginTerminal},
	"terminal":       {"Terminal", OriginTerminal}, // macOS Terminal.app
	"apple-terminal": {"Terminal", OriginTerminal},
	"wezterm":        {"WezTerm", OriginTerminal},
	"kitty":          {"Kitty", OriginTerminal},
	"alacritty":      {"Alacritty", OriginTerminal},
	"konsole":        {"Konsole", OriginTerminal},
	"gnome-terminal": {"GNOME Terminal", OriginTerminal},
	"xterm":          {"xterm", OriginTerminal},
	"terminator":     {"Terminator", OriginTerminal},
	"tmux":           {"tmux", OriginTerminal}, // best-effort when we can't see further up
	// IDEs
	"zed":      {"Zed", OriginIDE},
	"vscode":   {"VS Code", OriginIDE},
	"codium":   {"VSCodium", OriginIDE},
	"cursor":   {"Cursor", OriginIDE},
	"jetbrains": {"JetBrains", OriginIDE},
	// Desktop
	"claude-desktop": {"Claude Desktop", OriginDesktop},
}

// newOrigin looks up the catalog entry for a slug and returns a populated Origin.
// Falls back to Unknown if the slug is not registered.
func newOrigin(app string) Origin {
	if entry, ok := appCatalog[app]; ok {
		return Origin{Category: entry.Category, App: app, Display: entry.Display}
	}
	return Origin{}
}

// ProcessInfo describes one process in the ancestor chain.
// On Darwin, Exe holds whatever `ps -o comm=` returned (often the full
// "/Applications/Ghostty.app/Contents/MacOS/ghostty" path). On Linux it's
// the resolved /proc/<pid>/exe target, falling back to /proc/<pid>/comm
// when exe is not readable.
type ProcessInfo struct {
	PID  int
	Comm string // short command name (max 15 chars on Linux)
	Exe  string // full path if available
}

// classifyOrigin is a pure function that maps the detection signals (env
// variables and ancestor process chain) to an Origin. It must not touch the
// filesystem or spawn subprocesses so it can be unit-tested with synthetic
// input.
//
// Precedence (highest wins):
//  1. IDE env vars (Zed, VS Code, Cursor, JetBrains)
//  2. IDE ancestor bundle/exe match
//  3. Claude Desktop (bundle id or Claude.app ancestor)
//  4. Terminal env vars (TERM_PROGRAM and friends)
//  5. Terminal ancestor exe match
//  6. Unknown
func classifyOrigin(env map[string]string, ancestors []ProcessInfo) Origin {
	// 1. IDE env vars — checked first because an IDE-hosted terminal also
	// sets TERM_PROGRAM (sometimes to the IDE itself, sometimes to the host
	// terminal), so we look for IDE-specific markers explicitly.
	if _, ok := env["CURSOR_TRACE_ID"]; ok {
		return newOrigin("cursor")
	}
	if _, ok := env["VSCODE_INJECTION"]; ok {
		return vscodeVariant(env)
	}
	if _, ok := env["VSCODE_PID"]; ok {
		return vscodeVariant(env)
	}
	if tp := env["TERM_PROGRAM"]; tp == "vscode" {
		return vscodeVariant(env)
	}
	if _, ok := env["ZED_TERM"]; ok {
		return newOrigin("zed")
	}
	if tp := env["TERM_PROGRAM"]; tp == "zed" {
		return newOrigin("zed")
	}
	if _, ok := env["TERMINAL_EMULATOR"]; ok {
		if env["TERMINAL_EMULATOR"] == "JetBrains-JediTerm" {
			return newOrigin("jetbrains")
		}
	}

	// The first ancestor is usually the claude CLI process itself; what we
	// want is whatever spawned it, so skip index 0 for all ancestor scans.
	parents := ancestors
	if len(parents) > 0 {
		parents = parents[1:]
	}

	// 2. IDE ancestor match.
	for _, p := range parents {
		switch {
		case ancestorMatches(p, "Cursor"):
			return newOrigin("cursor")
		case ancestorMatches(p, "Zed"):
			return newOrigin("zed")
		case ancestorMatches(p, "Visual Studio Code", "Code", "Code - Insiders"):
			return newOrigin("vscode")
		case ancestorMatches(p, "VSCodium", "codium"):
			return newOrigin("codium")
		case ancestorMatches(p, "IntelliJ IDEA", "PyCharm", "WebStorm", "GoLand", "RubyMine", "PhpStorm", "CLion", "DataGrip", "Rider", "Android Studio"):
			return newOrigin("jetbrains")
		}
	}

	// 3. Terminal env vars. Checked before Claude Desktop because a real
	// terminal emulator always stamps TERM_PROGRAM / its own marker vars,
	// whereas Claude Desktop-spawned processes don't.
	if tp := env["TERM_PROGRAM"]; tp != "" {
		switch strings.ToLower(tp) {
		case "ghostty":
			return newOrigin("ghostty")
		case "iterm.app":
			return newOrigin("iterm")
		case "apple_terminal":
			return newOrigin("apple-terminal")
		case "wezterm":
			return newOrigin("wezterm")
		}
	}
	if _, ok := env["KITTY_WINDOW_ID"]; ok {
		return newOrigin("kitty")
	}
	if _, ok := env["ALACRITTY_WINDOW_ID"]; ok {
		return newOrigin("alacritty")
	}
	if _, ok := env["KONSOLE_VERSION"]; ok {
		return newOrigin("konsole")
	}
	if _, ok := env["WEZTERM_EXECUTABLE"]; ok {
		return newOrigin("wezterm")
	}
	if _, ok := env["GHOSTTY_RESOURCES_DIR"]; ok {
		return newOrigin("ghostty")
	}

	// 4. Terminal ancestor exe match.
	for _, p := range parents {
		switch {
		case ancestorMatches(p, "Ghostty"):
			return newOrigin("ghostty")
		case ancestorMatches(p, "iTerm", "iTerm2"):
			return newOrigin("iterm")
		case ancestorMatches(p, "Terminal"):
			return newOrigin("apple-terminal")
		case ancestorMatches(p, "WezTerm"):
			return newOrigin("wezterm")
		case ancestorMatches(p, "Alacritty", "alacritty"):
			return newOrigin("alacritty")
		case ancestorMatches(p, "kitty"):
			return newOrigin("kitty")
		case ancestorMatches(p, "konsole"):
			return newOrigin("konsole")
		case ancestorMatches(p, "gnome-terminal-server", "gnome-terminal"):
			return newOrigin("gnome-terminal")
		case ancestorMatches(p, "terminator"):
			return newOrigin("terminator")
		case ancestorMatches(p, "xterm"):
			return newOrigin("xterm")
		}
	}

	// 5. Claude Desktop — only if a proper Claude.app bundle shows up in
	// the ancestor chain, or the bundle id is explicitly set. A bare exe
	// named "claude" is the Claude CLI, not Claude Desktop.
	if env["__CFBundleIdentifier"] == "com.anthropic.claude" {
		return newOrigin("claude-desktop")
	}
	for _, p := range parents {
		if claudeDesktopAncestor(p) {
			return newOrigin("claude-desktop")
		}
	}

	return Origin{}
}

// claudeDesktopAncestor reports whether the ancestor looks like the
// "Claude Desktop" macOS app bundle (not the `claude` CLI exe).
func claudeDesktopAncestor(p ProcessInfo) bool {
	commLC := strings.ToLower(p.Comm)
	exeLC := strings.ToLower(p.Exe)
	return strings.Contains(commLC, "/claude.app/") || strings.Contains(exeLC, "/claude.app/")
}

// vscodeVariant distinguishes Cursor from vanilla VS Code when only VSCODE_*
// markers are present (Cursor sets them too).
func vscodeVariant(env map[string]string) Origin {
	if _, ok := env["CURSOR_TRACE_ID"]; ok {
		return newOrigin("cursor")
	}
	return newOrigin("vscode")
}

// DetectOrigin classifies the Claude process identified by pid.
// Returns a zero-valued Origin on any failure (the caller should treat that
// as "unknown" and neither display nor persist it).
func DetectOrigin(pid int) Origin {
	if pid <= 0 {
		return Origin{}
	}
	env := readProcessEnv(pid)
	chain := parentChain(pid)
	return classifyOrigin(env, chain)
}

// ancestorMatches reports whether the process's comm or exe path contains any
// of the given needle tokens (case-insensitive substring match). App bundle
// names like "Ghostty" match "/Applications/Ghostty.app/Contents/MacOS/ghostty".
func ancestorMatches(p ProcessInfo, needles ...string) bool {
	commLC := strings.ToLower(p.Comm)
	exeLC := strings.ToLower(p.Exe)
	for _, n := range needles {
		nLC := strings.ToLower(n)
		// Match bundle paths like ".../Ghostty.app/..." or bare exe like "ghostty".
		if strings.Contains(exeLC, "/"+nLC+".app/") {
			return true
		}
		if commLC == nLC || exeLC == nLC {
			return true
		}
		// /usr/bin/ghostty, /opt/zed/zed, etc.
		if strings.HasSuffix(exeLC, "/"+nLC) {
			return true
		}
		// macOS comm often already contains the full bundle path from `ps -o comm=`
		if strings.Contains(commLC, "/"+nLC+".app/") {
			return true
		}
	}
	return false
}
