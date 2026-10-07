package web

import (
	"regexp"
	"strings"
	"testing"
)

// app.js applies the route named by the URL hash on load by calling
// switchView, which reaches top-level bindings — a loader's `let` guard, the
// data it assigns into, the poll constants switchView itself reads. `let` and
// `const` bindings are hoisted without being initialised, so any one of them
// read before its declaration has run throws a ReferenceError. The loaders are
// async and nothing awaits them, so the throw becomes an unhandled rejection
// instead of an error that aborts init: the rest of the page loads normally
// and the only symptom is that one panel sits on "Loading" forever. Opening
// /#usage directly did exactly that (#95), and the flags feed reintroduced it
// the moment it got a guard of its own.
//
// The fix is positional — the init-from-hash call is the last thing in the
// IIFE, so every declaration it can reach has already run — and JavaScript
// enforces nothing about position. Two separate changes have tripped on this
// now, so the enforcement lives here. The test reads the embedded bytes
// rather than the file on disk, because those are what ships.
//
// `let`, `const` and `class` are the hazards: all three sit in the temporal
// dead zone until their declaration runs. `var` is hoisted as undefined, so a
// binding read early is merely falsy and the loader runs instead of throwing.
func TestNoTopLevelDeclarationAfterInitFromHash(t *testing.T) {
	src, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}

	lines := strings.Split(string(src), "\n")
	// Anchored on the binding, not on the hash read: #122's fix adds a
	// `hashchange` listener that reads the hash the same way, and matching
	// that instead would silently move the boundary to the top of the file.
	initLine := -1
	for i, line := range lines {
		if strings.Contains(line, "const initHash =") {
			initLine = i
			break
		}
	}
	if initLine == -1 {
		t.Fatal("app.js no longer reads the initial route into `const initHash`; " +
			"if that moved or was renamed, move this test with it")
	}

	// The IIFE's own statements sit at one indent level. Biome formats the
	// file with indentWidth 4 and CI checks it, so anything declared inside a
	// function is at eight spaces or more and cannot match.
	topLevelDecl := regexp.MustCompile(`^ {4}(let|const|class) `)

	// That indent width is a premise, and a silent one: set `indentWidth` to 2
	// in biome.jsonc and the pattern stops matching anything at all, so the
	// loop below finds no late declarations and passes forever — including on
	// a file that has the bug back. app.js declares dozens of bindings above
	// the init call, so zero matches there means the pattern, not the file,
	// is what changed.
	early := 0
	for i := 0; i < initLine; i++ {
		if topLevelDecl.MatchString(lines[i]) {
			early++
		}
	}
	if early == 0 {
		t.Fatal("found no top-level declarations anywhere above the init-from-hash call, " +
			"which cannot be true of app.js: the four-space indent this test matches on no longer " +
			"describes the file. Check `indentWidth` in biome.jsonc and update the pattern to match")
	}

	var late []string
	for i := initLine + 1; i < len(lines); i++ {
		if topLevelDecl.MatchString(lines[i]) {
			late = append(late, strings.TrimSpace(lines[i]))
		}
	}
	if len(late) > 0 {
		t.Errorf("app.js declares %d binding(s) after it applies the initial hash route:\n  %s\n"+
			"A loader the initial route reaches would read these before they are initialised and throw, "+
			"leaving that tab stuck on its loading state. Move the init-from-hash call back to the end of the IIFE.",
			len(late), strings.Join(late, "\n  "))
	}
}
