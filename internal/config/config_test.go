package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDefault_MatchesExpectedShape(t *testing.T) {
	d := Default()
	if !d.Interception.Kubectl || !d.Interception.K9s {
		t.Error("interception defaults should both be on")
	}
	if !d.Aliases.Opportunistic {
		t.Error("aliases.opportunistic default should be true")
	}
	if d.History.MaxUploadBytes != 256*1024 {
		t.Errorf("history.max_upload_bytes default = %d want %d", d.History.MaxUploadBytes, 256*1024)
	}
	if d.History.FZF != "fzf" {
		t.Errorf("history.fzf default = %q want fzf", d.History.FZF)
	}
}

func TestLoad_CreatesDefaultFileWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "konch", "config.toml")
	t.Setenv("KONCH_CONFIG", path)

	cfg, resolved, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if resolved != path {
		t.Errorf("Load() path = %q want %q", resolved, path)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Errorf("first-run cfg should equal Default(); got %+v", cfg)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected config file at %s, got: %v", path, err)
	}
}

func TestLoad_PartialOverrideKeepsDefaultsForOmittedFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(`
[interception]
kubectl = false

[aliases]
opportunistic = false
`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KONCH_CONFIG", path)

	cfg, _, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Interception.Kubectl {
		t.Error("expected kubectl=false from file")
	}
	if !cfg.Interception.K9s {
		t.Error("expected k9s=true (default) since file didn't set it")
	}
	if cfg.Aliases.Opportunistic {
		t.Error("expected opportunistic=false from file")
	}
	if cfg.History.MaxUploadBytes != 256*1024 {
		t.Errorf("history.max_upload_bytes should fall back to default, got %d", cfg.History.MaxUploadBytes)
	}
	if cfg.History.FZF != "fzf" {
		t.Errorf("history.fzf should fall back to default, got %q", cfg.History.FZF)
	}
}

func TestLoad_ParseError_ReturnsDefaultsAndError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("this is not toml ===="), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KONCH_CONFIG", path)

	cfg, _, err := Load()
	if err == nil {
		t.Error("expected parse error")
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Error("on parse error, caller should still get Default() values")
	}
}

func TestPath_HonorsKONCH_CONFIG(t *testing.T) {
	t.Setenv("KONCH_CONFIG", "/explicit/path.toml")
	got, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/explicit/path.toml" {
		t.Errorf("got %q", got)
	}
}

func TestPath_HonorsXDG_CONFIG_HOME(t *testing.T) {
	t.Setenv("KONCH_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	got, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/xdg/konch/config.toml" {
		t.Errorf("got %q", got)
	}
}
