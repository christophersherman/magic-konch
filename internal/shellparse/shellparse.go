// Package shellparse extracts aliases and simple exports from the user's
// shellrc files (~/.bashrc, ~/.zshrc, ~/.config/fish/config.fish). It is
// deliberately conservative: anything that requires running shell code to
// resolve ($-expansions, command substitution, glob expansion) is skipped
// with a reason so --probe can report what didn't translate.
//
// v0.2 phase A ships only the bash parser. Phase B adds zsh + fish.
package shellparse

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Result is the outcome of parsing one or more shellrc files.
type Result struct {
	Aliases []Alias
	Exports []Export
	Skipped []SkippedLine
	Sources []string // files actually read (existed + were parseable)
}

// Alias is a successfully parsed `alias NAME=VALUE` declaration.
type Alias struct {
	Name, Value, Path string
	Line              int
}

// Export is a successfully parsed `export NAME=VALUE` declaration.
type Export struct {
	Name, Value, Path string
	Line              int
}

// SkippedLine records a line we recognized as an alias/export but couldn't
// extract a literal value from. The reason is user-facing — it surfaces in
// --probe output.
type SkippedLine struct {
	Path, Source, Reason string
	Line                 int
}

var nameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ParseBash reads each path in order, extracting aliases and exports.
// Missing files are silently skipped. Read errors are returned, but a
// partial Result up to the failure point is still useful — most callers
// can ignore the error and use what we got.
func ParseBash(paths []string) (Result, error) {
	var r Result
	for _, p := range paths {
		if err := parseBashFile(p, &r); err != nil {
			return r, fmt.Errorf("%s: %w", p, err)
		}
	}
	return r, nil
}

// AutoDetectBashPaths returns the conventional bash sources for the
// current user, regardless of $SHELL. Callers in v0.2 phase A always
// pass these; phase B adds zsh/fish-aware detection.
func AutoDetectBashPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".bash_profile"),
	}
}

func parseBashFile(path string, r *Result) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	r.Sources = append(r.Sources, path)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	for sc.Scan() {
		lineNo++
		raw := sc.Text()
		trimmed := strings.TrimLeft(raw, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "alias "):
			parseAlias(path, lineNo, raw, trimmed[len("alias "):], r)
		case strings.HasPrefix(trimmed, "export "):
			parseExport(path, lineNo, raw, trimmed[len("export "):], r)
		}
	}
	return sc.Err()
}

func parseAlias(path string, lineNo int, raw, rest string, r *Result) {
	rest = strings.TrimLeft(rest, " \t")
	eq := strings.IndexByte(rest, '=')
	if eq < 0 {
		// `alias` (no `=`) lists existing aliases — not interesting.
		return
	}
	name := rest[:eq]
	if !nameRE.MatchString(name) {
		r.Skipped = append(r.Skipped, SkippedLine{
			Path: path, Line: lineNo, Source: raw,
			Reason: "alias name is not a simple identifier",
		})
		return
	}
	val, ok, reason := parseValue(rest[eq+1:])
	if !ok {
		r.Skipped = append(r.Skipped, SkippedLine{
			Path: path, Line: lineNo, Source: raw, Reason: reason,
		})
		return
	}
	r.Aliases = append(r.Aliases, Alias{Name: name, Value: val, Path: path, Line: lineNo})
}

func parseExport(path string, lineNo int, raw, rest string, r *Result) {
	rest = strings.TrimLeft(rest, " \t")
	eq := strings.IndexByte(rest, '=')
	if eq < 0 {
		// `export FOO` (already-set var) — not interesting since we
		// can't read its value from a static parse.
		return
	}
	name := rest[:eq]
	if !nameRE.MatchString(name) {
		r.Skipped = append(r.Skipped, SkippedLine{
			Path: path, Line: lineNo, Source: raw,
			Reason: "export name is not a simple identifier",
		})
		return
	}
	val, ok, reason := parseValue(rest[eq+1:])
	if !ok {
		r.Skipped = append(r.Skipped, SkippedLine{
			Path: path, Line: lineNo, Source: raw, Reason: reason,
		})
		return
	}
	r.Exports = append(r.Exports, Export{Name: name, Value: val, Path: path, Line: lineNo})
}

// parseValue extracts the literal value of an alias/export RHS. Three
// shapes are supported: single-quoted (literal), double-quoted (no $-
// or backtick-expansions), and bare single-word. Anything else gets
// rejected with a reason.
func parseValue(s string) (val string, ok bool, reason string) {
	if s == "" {
		return "", true, ""
	}
	switch s[0] {
	case '\'':
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return "", false, "unterminated single quote"
		}
		return s[1 : 1+end], true, ""
	case '"':
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			c := s[i]
			if c == '"' {
				return b.String(), true, ""
			}
			if c == '$' || c == '`' {
				return "", false, "value contains $-expansion or backtick (can't statically resolve)"
			}
			if c == '\\' && i+1 < len(s) {
				switch s[i+1] {
				case '"', '\\', '$', '`':
					b.WriteByte(s[i+1])
					i++
					continue
				}
			}
			b.WriteByte(c)
		}
		return "", false, "unterminated double quote"
	default:
		end := strings.IndexAny(s, " \t#")
		if end < 0 {
			end = len(s)
		}
		word := s[:end]
		if strings.ContainsAny(word, "$`*?[]<>|&;()") {
			return "", false, "value contains shell metacharacters"
		}
		return word, true, ""
	}
}
