package web

import (
	"regexp"
	"strings"
	"testing"
)

// app.js applies the route named by the URL hash on load by calling
// switchView, which calls that view's loader, which reads a `let` guard
// declared further down the IIFE. `let` and `const` bindings are hoisted
// without being initialised, so a loader reached before its guard's
// declaration has run throws a ReferenceError. The loaders are async and
// nothing awaits them, so the throw becomes an unhandled rejection instead of
// an error that aborts init: the rest of the page loads normally and the only
// symptom is that one panel sits on "Loading" forever. Opening /#usage
// directly did exactly that (#95), and the flags feed reintroduced it the
// moment it got a guard of its own.
//
// The fix is positional — the init-from-hash call is the last thing in the
// IIFE, so every declaration it can reach has already run — and JavaScript
// enforces nothing about position. Two separate changes have tripped on this
// now, so the enforcement lives here. The test reads the embedded bytes
// rather than the file on disk, because those are what ships.
//
// Only `let` and `const` are hazards. `var` is hoisted as undefined, so a
// guard read early is merely falsy and the loader runs instead of throwing.
func TestNoTopLevelDeclarationAfterInitFromHash(t *testing.T) {
	src, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}

	lines := strings.Split(string(src), "\n")
	initLine := -1
	for i, line := range lines {
		if strings.Contains(line, "window.location.hash.replace") {
			initLine = i
			break
		}
	}
	if initLine == -1 {
		t.Fatal("app.js no longer reads the initial route from window.location.hash; " +
			"if that moved, move this test with it")
	}

	// The IIFE's own statements sit at one indent level. Biome formats the
	// file with indentWidth 4 and CI checks it, so anything declared inside a
	// function is at eight spaces or more and cannot match.
	topLevelDecl := regexp.MustCompile(`^ {4}(let|const) `)

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
