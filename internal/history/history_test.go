package history

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRead_Missing(t *testing.T) {
	snap, err := Read(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatal(err)
	}
	if snap.FullSize != 0 || len(snap.ToShip) != 0 {
		t.Errorf("missing file should yield empty Snapshot, got %+v", snap)
	}
}

func TestRead_Small(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "h")
	content := []byte("alpha\nbeta\n")
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(snap.ToShip, content) {
		t.Errorf("ToShip mismatch")
	}
	if snap.FullSize != int64(len(content)) {
		t.Errorf("FullSize=%d want %d", snap.FullSize, len(content))
	}
}

func TestRead_TruncatesAtLineBoundary(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "h")
	// Build content larger than MaxBytesUploaded with predictable line shape.
	var b strings.Builder
	for i := 0; b.Len() < MaxBytesUploaded+8192; i++ {
		b.WriteString("line-of-history\n")
	}
	full := []byte(b.String())
	if err := os.WriteFile(p, full, 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(full)) != snap.FullSize {
		t.Errorf("FullSize=%d want %d", snap.FullSize, len(full))
	}
	if len(snap.ToShip) > MaxBytesUploaded {
		t.Errorf("ToShip too large: %d", len(snap.ToShip))
	}
	// First byte of ToShip should be the start of a line.
	if len(snap.ToShip) > 0 && snap.ToShip[0] == '\n' {
		t.Error("ToShip starts with newline — line-boundary trim broken")
	}
	// ToShip should be a clean tail of full.
	if !bytes.HasSuffix(full, snap.ToShip) {
		t.Error("ToShip is not a suffix of full content")
	}
}

func TestWriteMerge_Overwrite_WhenLocalFitsInToShip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "h")
	if err := os.WriteFile(p, []byte("a\nb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap := Snapshot{ToShip: []byte("a\nb\n"), FullSize: 4}
	pod := []byte("a\nb\nc\n")

	if err := WriteMerge(p, snap, pod); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if !bytes.Equal(got, pod) {
		t.Errorf("got %q want %q", got, pod)
	}
}

func TestWriteMerge_AppendsNewSuffix_WhenHeadTrimmedButPrefixMatches(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "h")
	// Local has a head we couldn't ship, plus a tail that became ToShip.
	head := []byte("HEAD-trimmed-line\n")
	shipped := []byte("shipped-line\n")
	original := append(append([]byte{}, head...), shipped...)
	if err := os.WriteFile(p, original, 0o600); err != nil {
		t.Fatal(err)
	}
	snap := Snapshot{ToShip: shipped, FullSize: int64(len(original))}
	// Pod returns shipped + new commands.
	newCmds := []byte("new-cmd-1\nnew-cmd-2\n")
	pod := append(append([]byte{}, shipped...), newCmds...)

	if err := WriteMerge(p, snap, pod); err != nil {
		t.Fatal(err)
	}
	want := append(append([]byte{}, original...), newCmds...)
	got, _ := os.ReadFile(p)
	if !bytes.Equal(got, want) {
		t.Errorf("\n got:  %q\n want: %q", got, want)
	}
}

func TestWriteMerge_FallsBackToOverwrite_WhenPrefixDoesNotMatch(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "h")
	original := []byte("HEAD\nshipped-line\n")
	if err := os.WriteFile(p, original, 0o600); err != nil {
		t.Fatal(err)
	}
	snap := Snapshot{ToShip: []byte("shipped-line\n"), FullSize: int64(len(original))}
	// Pod truncated to HISTFILESIZE — returns DIFFERENT content.
	pod := []byte("hist-trimmed\nnew-cmd\n")

	if err := WriteMerge(p, snap, pod); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if !bytes.Equal(got, pod) {
		t.Errorf("expected fallback overwrite\n got:  %q\n want: %q", got, pod)
	}
}

func TestWriteMerge_AtomicReplaceMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "deep", "nested", "h")
	snap := Snapshot{}
	if err := WriteMerge(p, snap, []byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm=%o want 0600", info.Mode().Perm())
	}
}

func TestLocalPath_SafeSegments(t *testing.T) {
	t.Setenv("KONCH_DATA_DIR", "/x")
	got, err := LocalPath("ctx", "ns", "Deployment/foo")
	if err != nil {
		t.Fatal(err)
	}
	want := "/x/history/ctx/ns/Deployment_foo"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestLocalPath_TraversalAttempt(t *testing.T) {
	t.Setenv("KONCH_DATA_DIR", "/x")
	got, _ := LocalPath("ctx", "../sneaky", "wl")
	if strings.Contains(got, "..") {
		t.Errorf("traversal got through: %q", got)
	}
}
