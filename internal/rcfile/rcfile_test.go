package rcfile

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoad_MergesContextOnTopOfBase(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KONCH_CONFIG_DIR", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	if err := os.WriteFile(filepath.Join(dir, "rc"), []byte("alias g='git'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rc.shermlabs"), []byte("export FOO=bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Load("shermlabs")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := "alias g='git'\nexport FOO=bar\n"
	if string(got.Bytes) != want {
		t.Errorf("merged bytes\n have: %q\n want: %q", got.Bytes, want)
	}
	if len(got.Sources) != 2 {
		t.Errorf("want 2 sources, got %v", got.Sources)
	}
	if got.Empty {
		t.Error("merged result should not be Empty")
	}
}

func TestLoad_BaseOnlyWhenContextMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KONCH_CONFIG_DIR", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	if err := os.WriteFile(filepath.Join(dir, "rc"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Load("does-not-exist")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(got.Bytes) != "hello\n" {
		t.Errorf("got %q", got.Bytes)
	}
}

func TestLoad_NoFilesMarksEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KONCH_CONFIG_DIR", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	got, err := Load("any")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !got.Empty {
		t.Error("Empty should be true when nothing was loaded")
	}
	if len(got.Bytes) != 0 {
		t.Errorf("Bytes should be zero-length, got %q", got.Bytes)
	}
}

func TestLoad_BaseWithoutTrailingNewlineGetsOneBeforeMerge(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KONCH_CONFIG_DIR", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	// no trailing newline on base — Konch must add one before appending
	if err := os.WriteFile(filepath.Join(dir, "rc"), []byte("export A=1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rc.k"), []byte("export B=2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, _ := Load("k")
	want := "export A=1\nexport B=2\n"
	if string(got.Bytes) != want {
		t.Errorf("\n have: %q\n want: %q", got.Bytes, want)
	}
}

func TestSkipForSh(t *testing.T) {
	in := []byte("alias g='git'\nexport FOO=1\nalias ll='ls -l'\n# comment\nfunction f() { :; }\n")
	out, skipped := SkipForSh(in)
	wantOut := "export FOO=1\n# comment\nfunction f() { :; }\n"
	if string(out) != wantOut {
		t.Errorf("\n have: %q\n want: %q", out, wantOut)
	}
	if !reflect.DeepEqual(skipped, []int{1, 3}) {
		t.Errorf("skipped lines: got %v want [1 3]", skipped)
	}
}

func TestSkipForSh_EmptyInput(t *testing.T) {
	out, skipped := SkipForSh(nil)
	if out != nil {
		t.Errorf("want nil out, got %q", out)
	}
	if skipped != nil {
		t.Errorf("want nil skipped, got %v", skipped)
	}
}

func TestSkipForSh_NoAliases(t *testing.T) {
	in := []byte("export X=y\nfoo() { :; }\n")
	out, skipped := SkipForSh(in)
	if string(out) != string(in) {
		t.Errorf("output should be unchanged, got %q", out)
	}
	if len(skipped) != 0 {
		t.Errorf("nothing should have been skipped, got %v", skipped)
	}
}
