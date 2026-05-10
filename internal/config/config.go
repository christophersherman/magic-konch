// Package config loads ~/.config/konch/config.toml. On first run the file
// is auto-created with commented-out defaults so users see the full schema
// without having to read the docs. Defaults are also exposed in-memory via
// Default() for tests and for the (rare) case where disk I/O fails.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is the resolved konch settings — defaults overlaid with any
// values from ~/.config/konch/config.toml.
type Config struct {
	Interception Interception `toml:"interception"`
	Shell        Shell        `toml:"shell"`
	Aliases      Aliases      `toml:"aliases"`
	History      History      `toml:"history"`
}

// Interception toggles each integration that rewrites another tool's
// pod-exec flow. Users can disable an integration without touching the
// shell or k9s configs that wired it.
type Interception struct {
	// Kubectl wrapper. Only relevant after `eval "$(konch init <shell>)"`.
	// Setting this false makes the wrapper a no-op; vanilla kubectl exec
	// runs untouched.
	Kubectl bool `toml:"kubectl"`
	// K9s integration. Only relevant after `konch setup k9s` has wired it.
	K9s bool `toml:"k9s"`
}

// Shell controls where konch reads aliases and simple exports from.
type Shell struct {
	// Sources is an explicit override list of absolute paths to read
	// aliases/exports from. Empty (the default) means auto-detect from
	// $SHELL: ~/.bashrc for bash, ~/.zshrc for zsh, etc.
	Sources []string `toml:"sources"`
}

// Aliases controls how aliases get injected into the pod.
type Aliases struct {
	// Opportunistic enables the in-pod fallback: for each alias X=Y,
	// probe `command -v <first-word-of-Y>` inside the pod; if missing,
	// unalias X so the system X (often the original GNU coreutils
	// command) still works. Costs one extra exec probe per session.
	Opportunistic bool `toml:"opportunistic"`
}

// History controls konch's local persistence and search.
type History struct {
	// MaxUploadBytes caps how much history we ship into the pod per
	// session via env vars. The apiserver enforces a URL-length limit;
	// uploads above this get tail-trimmed to a line boundary.
	MaxUploadBytes int `toml:"max_upload_bytes"`
	// FZF is the binary `konch history` shells out to for fuzzy search.
	// Set to "" to disable the `konch history` subcommand entirely.
	FZF string `toml:"fzf"`
}

// Default returns the in-memory defaults that match the auto-generated
// config file's contents. Use when disk I/O fails or in tests.
func Default() Config {
	return Config{
		Interception: Interception{Kubectl: true, K9s: true},
		Shell:        Shell{Sources: nil},
		Aliases:      Aliases{Opportunistic: true},
		History:      History{MaxUploadBytes: 256 * 1024, FZF: "fzf"},
	}
}

// Path returns the resolved path of the config file, honoring (in order):
// $KONCH_CONFIG, $XDG_CONFIG_HOME/konch/config.toml, ~/.config/konch/config.toml.
func Path() (string, error) {
	if p := os.Getenv("KONCH_CONFIG"); p != "" {
		return p, nil
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "konch", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "konch", "config.toml"), nil
}

// Load reads the config file, creating it with defaults on first run.
// Any field not set in the file falls back to the corresponding default.
// A missing or unreadable file is non-fatal: the caller still gets a
// usable Default() value, plus a non-nil warning where one exists.
func Load() (Config, string, error) {
	path, err := Path()
	if err != nil {
		return Default(), "", fmt.Errorf("resolve config path: %w", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		if writeErr := writeDefaults(path); writeErr != nil {
			// Couldn't create the file — caller still gets defaults.
			return Default(), path, nil
		}
		return Default(), path, nil
	}

	cfg := Default()
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return Default(), path, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, path, nil
}

// writeDefaults creates the config directory and writes a commented
// default file. We hand-write the contents (rather than toml.Marshal'ing
// Default()) so the file ships with explanatory comments — users open
// it once and learn the whole schema without docs.
func writeDefaults(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(defaultFile), 0o644)
}

const defaultFile = `# ~/.config/konch/config.toml — Magic Konch settings.
#
# Konch creates this file on first run. Defaults match what's commented
# out below; uncomment + edit any line to change behavior. You almost
# never need to.

[interception]
# kubectl = true   # the shell-function wrapper rewrites kubectl exec
# k9s     = true   # k9s integration (after ` + "`konch setup k9s`" + `)

[shell]
# Where to read aliases + simple exports from. Empty = auto-detect from
# $SHELL: ~/.bashrc for bash, ~/.zshrc for zsh, ~/.config/fish/config.fish
# for fish. Override with absolute paths if you keep aliases elsewhere.
# sources = ["/Users/you/dotfiles/aliases.sh"]

[aliases]
# Opportunistic fallback: for each alias X=Y, probe ` + "`command -v <first-word-of-Y>`" + `
# inside the pod; if missing, drop X so the system X still works.
# opportunistic = true

[history]
# Cap per-session upload size (apiserver URL length matters). 256 KiB
# is roughly 3000 commands of average shape.
# max_upload_bytes = 262144

# Local fuzzy-search binary for ` + "`konch history`" + `. Set to "" to disable.
# fzf = "fzf"
`
