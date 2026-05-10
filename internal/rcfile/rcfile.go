// Package rcfile loads and merges the user's per-context Konch rcfile.
//
// Konch reads ~/.config/konch/rc (always) and ~/.config/konch/rc.<context>
// (when the kubectl context name is supplied), concatenating in that order so
// the per-context file's lines win when bash sources the result. Konch never
// ships its own defaults; the user owns every line.
package rcfile

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Result is the outcome of loading a context's rcfile chain.
type Result struct {
	// Bytes is the merged rcfile content. Empty when no source files exist.
	Bytes []byte
	// Sources lists the absolute paths that contributed, in load order.
	Sources []string
	// Empty is true when nothing was loaded (the caller should skip --rcfile).
	Empty bool
}

// Load reads the base rcfile and, when kctx is non-empty, the per-context
// override, returning their concatenation. Missing files are skipped silently;
// only read errors other than ENOENT are returned.
func Load(kctx string) (Result, error) {
	base, err := configDir()
	if err != nil {
		return Result{}, err
	}
	candidates := []string{filepath.Join(base, "rc")}
	if kctx != "" {
		candidates = append(candidates, filepath.Join(base, "rc."+kctx))
	}
	var merged []byte
	var sources []string
	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return Result{}, fmt.Errorf("read rcfile %s: %w", p, err)
		}
		if len(b) == 0 {
			continue
		}
		if len(merged) > 0 && merged[len(merged)-1] != '\n' {
			merged = append(merged, '\n')
		}
		merged = append(merged, b...)
		sources = append(sources, p)
	}
	return Result{Bytes: merged, Sources: sources, Empty: len(merged) == 0}, nil
}

func configDir() (string, error) {
	if d := os.Getenv("KONCH_CONFIG_DIR"); d != "" {
		return d, nil
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "konch"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "konch"), nil
}

// aliasOnlyLine matches a line whose only non-comment content is `alias name=`.
// We strip these in sh-mode because POSIX sh has no alias-on-non-interactive,
// and the most common rcfile pollution that breaks `sh` is bash-style aliases.
// Anything more interesting (functions, exports, conditionals) is left alone
// and the user gets to deal with the consequences — mechanism, not opinion.
var aliasOnlyLine = regexp.MustCompile(`^\s*alias\s+[A-Za-z_][A-Za-z0-9_]*=`)

// SkipForSh returns the rcfile with bash-style alias lines removed and the
// 1-indexed source line numbers that were skipped. It is a courtesy filter for
// containers that only have /bin/sh; other constructs remain so the user can
// see what actually broke.
func SkipForSh(in []byte) (out []byte, skipped []int) {
	if len(in) == 0 {
		return in, nil
	}
	var b strings.Builder
	b.Grow(len(in))
	lines := strings.SplitAfter(string(in), "\n")
	for i, line := range lines {
		if aliasOnlyLine.MatchString(line) {
			skipped = append(skipped, i+1)
			continue
		}
		b.WriteString(line)
	}
	return []byte(b.String()), skipped
}
