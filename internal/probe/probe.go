// Package probe formats the --probe (dry-run) report. It explains exactly
// what Konch would do without doing it, so users can verify rcfile resolution,
// container selection, workload key, and the final exec command line before
// committing to an interactive session.
package probe

import (
	"fmt"
	"io"
	"strings"

	"github.com/christophersherman/magic-konch/internal/shell"
	"github.com/christophersherman/magic-konch/internal/workload"
)

// Report is the structured input to Render. Every field is human-facing.
type Report struct {
	Version         string
	Context         string
	Namespace       string
	Pod             string
	Container       string
	ContainerWhy    string // "default-container annotation" / "only container" / "--container flag"
	Shell           shell.Shell
	Workload        workload.Key
	HistoryPath     string
	HistoryToShip   int   // bytes that would be uploaded into the pod
	HistoryFullSize int64 // total bytes on disk (may exceed ToShip if capped)
	LocalTERM       string
	RCSources       []string // empty => no rcfile sourced
	SkippedShLines  []int    // lines suppressed in sh-mode (alias-only)
	FinalCommand    []string
}

// Render writes a multi-section probe report to w, ending with the Konch
// voice line. Output is plain text — friendly to grep, terminal width, and
// `--probe | tee` workflows.
func Render(w io.Writer, r Report) {
	p := func(format string, a ...any) { fmt.Fprintf(w, format+"\n", a...) }

	p("Konch %s dry-run — would exec into:", or(r.Version, "(dev)"))
	p("  context:    %s", or(r.Context, "(default)"))
	p("  namespace:  %s", r.Namespace)
	p("  pod:        %s", r.Pod)
	p("  container:  %s  (%s)", r.Container, r.ContainerWhy)
	p("")
	p("Resolved:")
	p("  shell:      %s", r.Shell)
	p("  workload:   %s", r.Workload)
	if r.HistoryFullSize > int64(r.HistoryToShip) {
		p("  history:    %s (%d bytes on disk; %d bytes would upload — head preserved on writeback)",
			r.HistoryPath, r.HistoryFullSize, r.HistoryToShip)
	} else {
		p("  history:    %s (%d bytes)", r.HistoryPath, r.HistoryFullSize)
	}
	if r.LocalTERM != "" {
		p("  TERM:       %s (passed through if pod has none)", r.LocalTERM)
	}
	p("")
	switch {
	case len(r.RCSources) == 0:
		p("rcfile:       (none — starting plain shell)")
	case r.Shell == shell.Sh:
		p("rcfile sources (found, but NOT sourced — sh-mode in v0.1):")
		for _, s := range r.RCSources {
			p("  - %s", s)
		}
	default:
		p("rcfile sources (merged in order, last wins):")
		for _, s := range r.RCSources {
			p("  - %s", s)
		}
	}
	if len(r.SkippedShLines) > 0 {
		p("")
		p("sh-mode would skip alias-only lines: %s", joinInts(r.SkippedShLines))
	}
	p("")
	p("Command that would be exec'd:")
	for i, c := range r.FinalCommand {
		prefix := "  "
		if i == 0 {
			prefix = "  $ "
		}
		p("%s%s", prefix, quoteForDisplay(c))
	}
	p("")
	p("~ the Konch has spoken ~")
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func joinInts(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = fmt.Sprintf("%d", n)
	}
	return strings.Join(parts, ", ")
}

// quoteForDisplay puts single quotes around args that contain whitespace or
// shell metacharacters, so the printed command is something a user could in
// principle paste. It is display-only; the actual exec sends the raw []string.
func quoteForDisplay(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\"'$`\\<>|&;()*?[]{}#~!") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
