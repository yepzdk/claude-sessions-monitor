package session

import (
	"os"
	"path/filepath"
	"testing"
)

// A running session whose log cannot be read must still be reported as
// running. It used to keep the Inactive default, and ActiveSessions drops
// Inactive sessions, so the row disappeared from the dashboard and from the
// summary counts while the process was still burning tokens.
func TestParseSessionKeepsRunningSessionWhenLogIsUnreadable(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(logFile, []byte(`{"type":"user"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(logFile, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(logFile, 0o644) })

	if f, err := os.Open(logFile); err == nil {
		f.Close()
		t.Skip("filesystem does not enforce permissions; cannot simulate a read failure")
	}

	s, err := parseSession("-home-u-proj", logFile, true, 4242)
	if err != nil {
		t.Fatalf("parseSession returned error: %v", err)
	}

	if s.Status == StatusInactive {
		t.Error("status is Inactive for a running process; the session would be " +
			"filtered out of the dashboard entirely")
	}
	if s.Status != StatusWaiting {
		t.Errorf("status = %q, want %q", s.Status, StatusWaiting)
	}
	if s.Degraded == "" {
		t.Error("Degraded is empty; the UI has no way to mark this row as incomplete")
	}
}

// An empty but readable log for a running process is a session that just
// started, not an idle one, and it is not degraded.
func TestParseSessionTreatsEmptyLogAsStartingUp(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(logFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := parseSession("-home-u-proj", logFile, true, 4242)
	if err != nil {
		t.Fatalf("parseSession returned error: %v", err)
	}
	if s.Status != StatusWaiting {
		t.Errorf("status = %q, want %q", s.Status, StatusWaiting)
	}
	if s.Degraded != "" {
		t.Errorf("Degraded = %q, want empty for a readable empty log", s.Degraded)
	}
}
