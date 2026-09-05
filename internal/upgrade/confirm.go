package upgrade

import (
	"bufio"
	"io"
	"strings"
)

// confirmer asks a yes/no question and reports whether it was answered yes.
type confirmer func(out io.Writer, question string) bool

// newPrompt builds the question csm asks before it replaces a binary.
//
// Two cases answer themselves and must not print the question at all:
//   - assumeYes: the user already said yes on the command line (-y).
//   - !interactive: nobody is there to type. A prompt nothing can answer would
//     hang CI and `curl | sh`-style installs, which is worse than not asking.
//
// Anything other than y/yes declines, so a bare Enter, an EOF and a typo all
// mean "leave the binary alone" -- the safe reading of an ambiguous answer.
func newPrompt(in io.Reader, interactive, assumeYes bool) confirmer {
	return func(out io.Writer, question string) bool {
		if assumeYes || !interactive {
			return true
		}
		printf(out, "%s [y/N] ", question)

		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && line == "" {
			printf(out, "\n")
			return false
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true
		default:
			return false
		}
	}
}
