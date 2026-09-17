package session

import "strings"

// A very small shell reader: enough to tell what a command *runs* from what it
// merely *contains*.
//
// This exists because the naive thing does not work. Matching a pattern against
// the raw command string flags 8% of every Bash call in a real corpus, almost
// all of it wrong: `rm -rf` inside a heredoc that is writing documentation, the
// word "credentials" inside a scratch directory path, `curl -X POST` against
// localhost in an echoed explanation. Splitting the command into the argument
// vectors it actually executes first, and classifying those, takes the same rule
// set from 8% to 1.5% -- and what is left is genuinely notable.
//
// It is a reader, not an interpreter: no expansion, no substitution semantics,
// no arithmetic. Where the two differ it errs towards showing the rule more
// text, never less, because a missed segment is a missed flag.

// shellSegment is one command position in a shell line: the words it runs, plus
// whether its output feeds the next segment.
type shellSegment struct {
	Argv []string
	// Pipe is true when this segment was terminated by `|`. It is what lets a
	// rule tell `curl … | sh` from `curl …; sh`, which is the difference between
	// running a downloaded script and running two unrelated commands.
	Pipe bool
}

// Command returns the segment's command name with any directory stripped, and
// the arguments after it. Leading `VAR=value` assignments are skipped: they are
// environment for the command, not the command.
//
// The basename is compared, never a substring of the whole word, so
// `/usr/local/bin/rm` is rm and `./scripts/sudo-helper` is not sudo.
func (s shellSegment) Command() (string, []string) {
	i := 0
	for i < len(s.Argv) && isAssignment(s.Argv[i]) {
		i++
	}
	if i >= len(s.Argv) {
		return "", nil
	}
	name := s.Argv[i]
	if j := strings.LastIndexByte(name, '/'); j >= 0 {
		name = name[j+1:]
	}
	return name, s.Argv[i+1:]
}

// isAssignment reports whether a word is a `NAME=value` environment prefix.
func isAssignment(word string) bool {
	eq := strings.IndexByte(word, '=')
	if eq <= 0 {
		return false
	}
	for i := range eq {
		if !isNameByte(word[i]) {
			return false
		}
	}
	return true
}

// isNameByte reports whether a byte may appear in a shell variable name.
func isNameByte(c byte) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}

// splitCommand reads a shell command into the segments it runs.
//
// What it deliberately drops:
//
//   - heredoc bodies, which are data the command is fed, not commands. This is
//     the single biggest source of false positives: agents write files by
//     heredoc constantly, and those files are full of shell examples.
//   - redirection targets, so `echo x > sudo` cannot be read as running sudo.
//   - quoting, after using it. A quoted word is one word; a quoted `rm -rf` in
//     an echo is one word and matches no rule.
//
// What it deliberately keeps: the inside of `$( … )` and `( … )`, because a
// command substitution runs its contents.
func splitCommand(cmd string) []shellSegment {
	p := shellReader{src: cmd}
	return p.run()
}

type shellReader struct {
	src      string
	i        int
	word     strings.Builder
	argv     []string
	segments []shellSegment
	// heredocs holds delimiters whose bodies start at the next newline. A single
	// line can open more than one (`cmd <<A <<B`).
	heredocs []heredoc
}

type heredoc struct {
	delim  string
	indent bool // <<- strips leading tabs from the terminator
}

func (p *shellReader) run() []shellSegment {
	for p.i < len(p.src) {
		switch c := p.src[p.i]; {
		case c == '\'':
			p.readSingleQuoted()
		case c == '"':
			p.readDoubleQuoted()
		case c == '\\':
			p.readEscape()
		case c == '<' || c == '>':
			p.readRedirect()
		case c == '\n':
			p.i++
			p.endSegment(false)
			p.skipHeredocBodies()
		case c == '|':
			// `||` is a separator like `;`; a single `|` is a pipe.
			if p.i+1 < len(p.src) && p.src[p.i+1] == '|' {
				p.i += 2
				p.endSegment(false)
				continue
			}
			p.i++
			p.endSegment(true)
		case c == ';' || c == '&' || c == '(' || c == ')' || c == '{' || c == '}':
			p.i++
			p.endSegment(false)
		case c == '$' && p.i+1 < len(p.src) && p.src[p.i+1] == '(':
			// Step past `$(` so the substitution's contents are read as their
			// own segment rather than glued to the word being built.
			p.i += 2
			p.endSegment(false)
		case c == ' ' || c == '\t' || c == '\r':
			p.i++
			p.endWord()
		default:
			p.word.WriteByte(c)
			p.i++
		}
	}
	p.endSegment(false)
	return p.segments
}

func (p *shellReader) endWord() {
	if p.word.Len() > 0 {
		p.argv = append(p.argv, p.word.String())
		p.word.Reset()
	}
}

func (p *shellReader) endSegment(pipe bool) {
	p.endWord()
	if len(p.argv) > 0 {
		p.segments = append(p.segments, shellSegment{Argv: p.argv, Pipe: pipe})
		p.argv = nil
	}
}

// readSingleQuoted copies a '…' run verbatim. Nothing escapes inside it, not
// even a backslash.
func (p *shellReader) readSingleQuoted() {
	p.i++
	for p.i < len(p.src) && p.src[p.i] != '\'' {
		p.word.WriteByte(p.src[p.i])
		p.i++
	}
	p.i++ // the closing quote, or one past the end of an unterminated string
	// A quoted run is part of a word even when empty, so `rm ''` keeps its
	// second argument rather than dropping it.
	if p.word.Len() == 0 {
		p.argv = append(p.argv, "")
	}
}

// readDoubleQuoted copies a "…" run, honouring backslash escapes.
func (p *shellReader) readDoubleQuoted() {
	p.i++
	empty := p.word.Len() == 0
	for p.i < len(p.src) && p.src[p.i] != '"' {
		if p.src[p.i] == '\\' && p.i+1 < len(p.src) {
			p.i++
		}
		p.word.WriteByte(p.src[p.i])
		p.i++
	}
	p.i++
	if empty && p.word.Len() == 0 {
		p.argv = append(p.argv, "")
	}
}

// readEscape consumes a backslash and the character it protects. A backslash
// before a newline is a line continuation and joins the two lines.
func (p *shellReader) readEscape() {
	p.i++
	if p.i >= len(p.src) {
		return
	}
	if p.src[p.i] != '\n' {
		p.word.WriteByte(p.src[p.i])
	}
	p.i++
}

// readRedirect consumes a redirection operator and its target. `<<word` opens a
// heredoc whose body is skipped at the next newline; `<<<` is a here-string,
// whose operand is data.
func (p *shellReader) readRedirect() {
	// `2>&1` prefixes the operator with a file descriptor. Adjacency is what
	// makes it one: `cmd 2 > f` really does pass 2 as an argument, and there the
	// space has already flushed the word.
	if allDigits(p.word.String()) {
		p.word.Reset()
	}
	p.endWord()
	if p.src[p.i] == '<' && strings.HasPrefix(p.src[p.i:], "<<") && !strings.HasPrefix(p.src[p.i:], "<<<") {
		p.readHeredocDelimiter()
		return
	}
	for p.i < len(p.src) && (p.src[p.i] == '<' || p.src[p.i] == '>' || p.src[p.i] == '&') {
		p.i++
	}
	p.skipSpaces()
	// The target is a filename, so it is read and thrown away rather than left
	// to become a segment of its own.
	for p.i < len(p.src) && !isShellBreak(p.src[p.i]) {
		if p.src[p.i] == '\'' || p.src[p.i] == '"' {
			q := p.src[p.i]
			p.i++
			for p.i < len(p.src) && p.src[p.i] != q {
				p.i++
			}
		}
		p.i++
	}
}

// allDigits reports whether a non-empty string is only decimal digits.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func (p *shellReader) readHeredocDelimiter() {
	p.i += 2
	h := heredoc{}
	if p.i < len(p.src) && p.src[p.i] == '-' {
		h.indent = true
		p.i++
	}
	p.skipSpaces()
	var delim strings.Builder
	for p.i < len(p.src) && !isShellBreak(p.src[p.i]) {
		c := p.src[p.i]
		if c == '\'' || c == '"' {
			p.i++
			continue
		}
		delim.WriteByte(c)
		p.i++
	}
	h.delim = delim.String()
	if h.delim != "" {
		p.heredocs = append(p.heredocs, h)
	}
}

// skipHeredocBodies consumes every open heredoc's body, which begins at the
// line the reader is now on.
func (p *shellReader) skipHeredocBodies() {
	for _, h := range p.heredocs {
		for p.i < len(p.src) {
			line, next := p.readRawLine()
			p.i = next
			term := line
			if h.indent {
				term = strings.TrimLeft(term, "\t")
			}
			if term == h.delim {
				break
			}
		}
	}
	p.heredocs = nil
}

// readRawLine returns the line starting at the reader's position and the index
// just past its newline.
func (p *shellReader) readRawLine() (string, int) {
	end := strings.IndexByte(p.src[p.i:], '\n')
	if end < 0 {
		return p.src[p.i:], len(p.src)
	}
	return p.src[p.i : p.i+end], p.i + end + 1
}

func (p *shellReader) skipSpaces() {
	for p.i < len(p.src) && (p.src[p.i] == ' ' || p.src[p.i] == '\t') {
		p.i++
	}
}

// isShellBreak reports whether a byte ends an unquoted word.
func isShellBreak(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', ';', '|', '&', '(', ')', '<', '>':
		return true
	}
	return false
}
