package session

import "testing"

func TestClassifyOrigin(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		ancestors []ProcessInfo
		want      Origin
	}{
		{
			name: "ghostty via TERM_PROGRAM",
			env: map[string]string{
				"TERM_PROGRAM":          "ghostty",
				"GHOSTTY_RESOURCES_DIR": "/Applications/Ghostty.app/Contents/Resources/ghostty",
				"__CFBundleIdentifier":  "com.mitchellh.ghostty",
			},
			ancestors: []ProcessInfo{
				{PID: 99, Comm: "-/bin/zsh"},
				{PID: 42, Comm: "/Applications/Ghostty.app/Contents/MacOS/ghostty"},
			},
			want: Origin{Category: OriginTerminal, App: "ghostty", Display: "Ghostty"},
		},
		{
			name: "iterm via TERM_PROGRAM",
			env: map[string]string{
				"TERM_PROGRAM": "iTerm.app",
				"LC_TERMINAL":  "iTerm2",
			},
			want: Origin{Category: OriginTerminal, App: "iterm", Display: "iTerm"},
		},
		{
			name: "apple terminal via TERM_PROGRAM",
			env: map[string]string{
				"TERM_PROGRAM": "Apple_Terminal",
			},
			want: Origin{Category: OriginTerminal, App: "apple-terminal", Display: "Terminal"},
		},
		{
			name: "terminal ancestor fallback when no env",
			env:  map[string]string{},
			ancestors: []ProcessInfo{
				{PID: 1001, Comm: "claude"}, // skipped
				{PID: 99, Comm: "zsh"},
				{PID: 42, Exe: "/Applications/Ghostty.app/Contents/MacOS/ghostty"},
			},
			want: Origin{Category: OriginTerminal, App: "ghostty", Display: "Ghostty"},
		},
		{
			// Regression for live macOS Ghostty+Claude chain where the bare
			// "claude" comm was previously misclassifying as Claude Desktop.
			name: "ghostty wins over bare claude CLI in chain",
			env: map[string]string{
				"TERM_PROGRAM":         "ghostty",
				"__CFBundleIdentifier": "com.mitchellh.ghostty",
			},
			ancestors: []ProcessInfo{
				{PID: 9034, Comm: "claude", Exe: "claude"},
				{PID: 53621, Comm: "-/bin/zsh", Exe: "-/bin/zsh"},
				{PID: 53620, Comm: "/usr/bin/login", Exe: "/usr/bin/login"},
				{PID: 1213, Comm: "/Applications/Ghostty.app/Contents/MacOS/ghostty", Exe: "/Applications/Ghostty.app/Contents/MacOS/ghostty"},
			},
			want: Origin{Category: OriginTerminal, App: "ghostty", Display: "Ghostty"},
		},
		{
			name: "zed via ZED_TERM",
			env: map[string]string{
				"ZED_TERM":     "true",
				"TERM_PROGRAM": "zed",
			},
			want: Origin{Category: OriginIDE, App: "zed", Display: "Zed"},
		},
		{
			name: "zed via ancestor on linux",
			env:  map[string]string{},
			ancestors: []ProcessInfo{
				{PID: 1001, Comm: "claude"},
				{PID: 99, Comm: "zsh"},
				{PID: 42, Exe: "/opt/zed/zed"},
			},
			want: Origin{Category: OriginIDE, App: "zed", Display: "Zed"},
		},
		{
			name: "vscode via VSCODE_INJECTION",
			env: map[string]string{
				"VSCODE_INJECTION": "1",
				"TERM_PROGRAM":     "vscode",
			},
			want: Origin{Category: OriginIDE, App: "vscode", Display: "VS Code"},
		},
		{
			name: "cursor via CURSOR_TRACE_ID overrides vscode",
			env: map[string]string{
				"VSCODE_INJECTION": "1",
				"CURSOR_TRACE_ID":  "abc123",
				"TERM_PROGRAM":     "vscode",
			},
			want: Origin{Category: OriginIDE, App: "cursor", Display: "Cursor"},
		},
		{
			name: "claude desktop via bundle id",
			env: map[string]string{
				"__CFBundleIdentifier": "com.anthropic.claude",
			},
			want: Origin{Category: OriginDesktop, App: "claude-desktop", Display: "Claude Desktop"},
		},
		{
			name: "claude desktop via ancestor",
			env:  map[string]string{},
			ancestors: []ProcessInfo{
				{PID: 1001, Comm: "claude"}, // the Claude CLI itself — skipped
				{PID: 42, Comm: "/Applications/Claude.app/Contents/MacOS/Claude"},
			},
			want: Origin{Category: OriginDesktop, App: "claude-desktop", Display: "Claude Desktop"},
		},
		{
			name: "ide env wins over terminal ancestor",
			env: map[string]string{
				"TERM_PROGRAM":     "ghostty",
				"VSCODE_INJECTION": "1",
			},
			ancestors: []ProcessInfo{
				{PID: 1001, Comm: "claude"},
				{PID: 42, Exe: "/Applications/Ghostty.app/Contents/MacOS/ghostty"},
			},
			want: Origin{Category: OriginIDE, App: "vscode", Display: "VS Code"},
		},
		{
			name: "kitty via env",
			env: map[string]string{
				"KITTY_WINDOW_ID": "1",
				"TERM":            "xterm-kitty",
			},
			want: Origin{Category: OriginTerminal, App: "kitty", Display: "Kitty"},
		},
		{
			name: "alacritty via env",
			env: map[string]string{
				"ALACRITTY_WINDOW_ID": "0x1234",
			},
			want: Origin{Category: OriginTerminal, App: "alacritty", Display: "Alacritty"},
		},
		{
			name: "wezterm via env",
			env: map[string]string{
				"TERM_PROGRAM":       "WezTerm",
				"WEZTERM_EXECUTABLE": "/usr/local/bin/wezterm",
			},
			want: Origin{Category: OriginTerminal, App: "wezterm", Display: "WezTerm"},
		},
		{
			name: "gnome terminal via ancestor",
			env:  map[string]string{"VTE_VERSION": "7600"},
			ancestors: []ProcessInfo{
				{PID: 1001, Comm: "claude"}, // the Claude CLI itself — skipped
				{PID: 42, Exe: "/usr/libexec/gnome-terminal-server"},
			},
			want: Origin{Category: OriginTerminal, App: "gnome-terminal", Display: "GNOME Terminal"},
		},
		{
			name: "jetbrains via TERMINAL_EMULATOR",
			env: map[string]string{
				"TERMINAL_EMULATOR": "JetBrains-JediTerm",
			},
			want: Origin{Category: OriginIDE, App: "jetbrains", Display: "JetBrains"},
		},
		{
			name:      "unknown when nothing matches",
			env:       map[string]string{"TERM": "xterm-256color"},
			ancestors: []ProcessInfo{{PID: 1, Comm: "launchd"}},
			want:      Origin{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyOrigin(tt.env, tt.ancestors)
			if got != tt.want {
				t.Errorf("classifyOrigin() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestOriginIsZero(t *testing.T) {
	if !(Origin{}).IsZero() {
		t.Errorf("zero Origin should report IsZero() == true")
	}
	if (Origin{Category: OriginTerminal, App: "ghostty", Display: "Ghostty"}).IsZero() {
		t.Errorf("populated Origin should report IsZero() == false")
	}
}

func TestNewOriginUnknownSlug(t *testing.T) {
	o := newOrigin("no-such-app")
	if !o.IsZero() {
		t.Errorf("newOrigin with unknown slug should return zero Origin, got %+v", o)
	}
}
