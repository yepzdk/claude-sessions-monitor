package session

import (
	"path/filepath"
	"testing"
)

func TestOriginStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	originStoreDirFn = func() (string, error) { return dir, nil }
	t.Cleanup(func() { originStoreDirFn = defaultOriginStoreDir })

	sid := "d3adbeef-0000-1111-2222-aaaabbbbcccc"
	want := Origin{Category: OriginTerminal, App: "ghostty", Display: "Ghostty"}

	if err := SaveOrigin(sid, want); err != nil {
		t.Fatalf("SaveOrigin: %v", err)
	}

	got, ok := LoadOrigin(sid)
	if !ok {
		t.Fatalf("LoadOrigin returned ok=false after save")
	}
	if got != want {
		t.Errorf("LoadOrigin = %+v, want %+v", got, want)
	}

	// File must exist at the expected path.
	if _, err := filepath.Abs(filepath.Join(dir, sid+".json")); err != nil {
		t.Fatalf("expected file path unreachable: %v", err)
	}
}

func TestOriginStoreSkipsEmpty(t *testing.T) {
	dir := t.TempDir()
	originStoreDirFn = func() (string, error) { return dir, nil }
	t.Cleanup(func() { originStoreDirFn = defaultOriginStoreDir })

	// Zero origin should be a no-op.
	if err := SaveOrigin("sid", Origin{}); err != nil {
		t.Fatalf("SaveOrigin with zero origin: %v", err)
	}
	if _, ok := LoadOrigin("sid"); ok {
		t.Fatalf("LoadOrigin should return ok=false for never-saved id")
	}

	// Empty session id should be a no-op.
	if err := SaveOrigin("", Origin{Category: OriginTerminal, App: "ghostty", Display: "Ghostty"}); err != nil {
		t.Fatalf("SaveOrigin with empty sid: %v", err)
	}
}

func TestLoadOriginMissing(t *testing.T) {
	dir := t.TempDir()
	originStoreDirFn = func() (string, error) { return dir, nil }
	t.Cleanup(func() { originStoreDirFn = defaultOriginStoreDir })

	if _, ok := LoadOrigin("no-such-id"); ok {
		t.Errorf("LoadOrigin of missing id should return ok=false")
	}
}
