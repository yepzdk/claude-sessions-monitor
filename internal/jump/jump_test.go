package jump

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestResultMessageNamesThePlatformsNoun(t *testing.T) {
	// The shared Result used to hard-code "tabs", which is wrong on Linux:
	// Matches counts compositor windows there, and csm cannot see tabs at all.
	linux := Result{Matches: 3, Noun: "window", Name: "◐ Fixing the parser"}
	if got := linux.Message(); !strings.Contains(got, "3 windows matched") {
		t.Errorf("Message() = %q, want it to count windows", got)
	}

	darwin := Result{Matches: 2, Noun: "tab", Name: "✳ Fix a bug"}
	if got := darwin.Message(); !strings.Contains(got, "2 tabs matched") {
		t.Errorf("Message() = %q, want it to count tabs", got)
	}
}

func TestResultMessageShowsWhatAGuessChose(t *testing.T) {
	// A guess among several candidates is only checkable if the user can see
	// which one was raised.
	r := Result{Matches: 3, Noun: "window", Name: "htop"}
	if got := r.Message(); !strings.Contains(got, "htop") {
		t.Errorf("Message() = %q, want the chosen title so a wrong pick is visible", got)
	}
}

func TestResultMessageFlagsAnUncertainPairing(t *testing.T) {
	// Exactly one window matched, but the pid it was found from was paired to
	// the session positionally. The window search being exact does not make
	// the answer exact, so the message must not read like a certainty.
	guess := Result{Matches: 1, Noun: "window", Name: "~/proj", Guessed: true}
	got := guess.Message()
	if !strings.Contains(got, "guess") {
		t.Errorf("Message() = %q, want it to admit the pairing was a guess", got)
	}

	sure := Result{Matches: 1, Noun: "window", Name: "~/proj"}
	if got := sure.Message(); got != "Focused ~/proj" {
		t.Errorf("Message() = %q, want a plain confirmation for a certain match", got)
	}
}

func TestResultMessageWithNothingToGoOn(t *testing.T) {
	// A terminal that reports no title must still produce a sentence.
	if got := (Result{}).Message(); got != "Focused session" {
		t.Errorf("Message() = %q", got)
	}
}

func TestStderrLineExtractsTheRealComplaint(t *testing.T) {
	// ExitError.Error() is only "exit status N"; the reason a compositor client
	// refused is on stderr, and dropping it left users with nothing to act on.
	// Output(), which run() uses, is what captures it.
	_, err := exec.Command("sh", "-c", "echo 'Cannot open display' >&2; echo 'and more' >&2; exit 1").Output()
	if err == nil {
		t.Fatal("expected the helper command to fail")
	}
	if got := stderrLine(err); got != "Cannot open display" {
		t.Errorf("stderrLine() = %q, want the first stderr line only", got)
	}
}

func TestStderrLineWithoutStderr(t *testing.T) {
	if got := stderrLine(errors.New("plain")); got != "" {
		t.Errorf("stderrLine() = %q, want empty so the caller can fall back", got)
	}
	_, err := exec.Command("sh", "-c", "exit 3").Output()
	if got := stderrLine(err); got != "" {
		t.Errorf("stderrLine() = %q, want empty for a silent failure", got)
	}
}
