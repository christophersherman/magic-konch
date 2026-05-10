package shellparse

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseBash_HandlesAllAliasFlavors(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, ".bashrc", `
# a comment
alias k=kubectl
alias gs='git status'
alias ll="ls -la"
   alias       indented='foo bar'
alias with_underscore='ok'
alias has1number='ok'
`)

	r, err := ParseBash([]string{p})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"k":               "kubectl",
		"gs":              "git status",
		"ll":              "ls -la",
		"indented":        "foo bar",
		"with_underscore": "ok",
		"has1number":      "ok",
	}
	if len(r.Aliases) != len(want) {
		t.Fatalf("got %d aliases, want %d: %+v", len(r.Aliases), len(want), r.Aliases)
	}
	for _, a := range r.Aliases {
		if want[a.Name] != a.Value {
			t.Errorf("alias %q: got %q want %q", a.Name, a.Value, want[a.Name])
		}
	}
	if len(r.Skipped) != 0 {
		t.Errorf("expected no skipped lines, got %+v", r.Skipped)
	}
}

func TestParseBash_SkipsExpansionsWithReasons(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, ".bashrc",
		"alias ohnodate=$(date)\n"+
			"alias dynamic=\"hello $USER\"\n"+
			"alias hasbackticks=\"echo `whoami`\"\n"+
			"alias unterminated='oops\n")

	r, err := ParseBash([]string{p})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Aliases) != 0 {
		t.Errorf("expected 0 aliases, got %+v", r.Aliases)
	}
	if len(r.Skipped) < 3 {
		t.Errorf("expected at least 3 skipped lines, got %+v", r.Skipped)
	}
	// Each skipped line should have a non-empty Reason.
	for _, s := range r.Skipped {
		if s.Reason == "" {
			t.Errorf("skipped line has empty reason: %+v", s)
		}
	}
}

func TestParseBash_ExtractsSimpleExports(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, ".bashrc", `
export EDITOR=vim
export GREP_OPTIONS='--color=auto'
export PAGER="less -R"
export PATH=$PATH:/foo     # dynamic — should skip
export                     # no rhs — not interesting, no skip
`)

	r, err := ParseBash([]string{p})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"EDITOR":       "vim",
		"GREP_OPTIONS": "--color=auto",
		"PAGER":        "less -R",
	}
	if len(r.Exports) != len(want) {
		t.Errorf("got %d exports, want %d: %+v", len(r.Exports), len(want), r.Exports)
	}
	for _, e := range r.Exports {
		if want[e.Name] != e.Value {
			t.Errorf("export %q: got %q want %q", e.Name, e.Value, want[e.Name])
		}
	}
	// PATH=$PATH:/foo should be skipped with a reason.
	foundPATHSkip := false
	for _, s := range r.Skipped {
		if s.Line == 5 {
			foundPATHSkip = true
		}
	}
	if !foundPATHSkip {
		t.Errorf("expected PATH=$PATH:/foo to land in Skipped, got %+v", r.Skipped)
	}
}

func TestParseBash_MissingFilesAreNonFatal(t *testing.T) {
	r, err := ParseBash([]string{"/this/path/does/not/exist"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.Aliases)+len(r.Exports)+len(r.Sources) != 0 {
		t.Errorf("expected nothing from missing path, got %+v", r)
	}
}

func TestParseBash_RecordsSourcesActuallyRead(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, ".bashrc", "alias x=y\n")
	b := writeFile(t, dir, ".bash_profile", "alias z=w\n")
	missing := filepath.Join(dir, "does-not-exist")

	r, _ := ParseBash([]string{a, missing, b})
	if len(r.Sources) != 2 {
		t.Errorf("Sources should record only files that existed, got %+v", r.Sources)
	}
}

func TestParseBash_IgnoresAliasListAndExportLookup(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, ".bashrc", "alias\nexport\n")
	r, _ := ParseBash([]string{p})
	if len(r.Aliases) != 0 || len(r.Exports) != 0 || len(r.Skipped) != 0 {
		t.Errorf("`alias` / `export` with no rhs should be silently ignored, got %+v", r)
	}
}

func TestAutoDetectPaths_BashShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/bash")
	got := AutoDetectPaths()
	if len(got) != 2 {
		t.Errorf("expected 2 bash paths, got %v", got)
	}
	if filepath.Base(got[0]) != ".bashrc" {
		t.Errorf("expected .bashrc first, got %q", got[0])
	}
}

func TestAutoDetectPaths_ZshShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	got := AutoDetectPaths()
	if len(got) != 2 {
		t.Errorf("expected 2 zsh paths, got %v", got)
	}
	if filepath.Base(got[0]) != ".zshrc" {
		t.Errorf("expected .zshrc first, got %q", got[0])
	}
}

func TestAutoDetectPaths_FishShellEmpty(t *testing.T) {
	t.Setenv("SHELL", "/usr/local/bin/fish")
	got := AutoDetectPaths()
	if len(got) != 0 {
		t.Errorf("fish should return no paths (phase B), got %v", got)
	}
}
