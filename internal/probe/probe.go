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
	ShellrcSources  []string         // shellrc files actually read
	AliasesImported int              // count of aliases successfully parsed
	ExportsImported int              // count of simple exports successfully parsed
	ShellrcSkipped  []SkippedShellrc // parser couldn't extract a literal value
	Opportunistic   bool             // whether the in-pod fallback is on
	FinalCommand    []string
}

// SkippedShellrc is the probe-level view of a shellparse.SkippedLine.
// Kept narrow so package probe doesn't have to depend on shellparse.
type SkippedShellrc struct {
	Path, Reason string
	Line         int
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
	case len(r.ShellrcSources) == 0:
		p("shellrc:      (none found — your aliases won't be imported)")
	case r.Shell == shell.Sh:
		p("shellrc:      found but NOT sourced (sh-mode; bash required for alias injection)")
		for _, s := range r.ShellrcSources {
			p("  - %s", s)
		}
	default:
		p("shellrc sources:")
		for _, s := range r.ShellrcSources {
			p("  - %s", s)
		}
		p("imported:     %d aliases, %d exports%s",
			r.AliasesImported, r.ExportsImported,
			ternary(r.Opportunistic, " (with opportunistic fallback)", ""))
	}
	if len(r.ShellrcSkipped) > 0 {
		p("")
		p("shellrc lines konch couldn't translate (%d):", len(r.ShellrcSkipped))
		maxShow := 5
		for i, s := range r.ShellrcSkipped {
			if i >= maxShow {
				p("  ... %d more", len(r.ShellrcSkipped)-maxShow)
				break
			}
			p("  - %s:%d  %s", s.Path, s.Line, s.Reason)
		}
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

func ternary(cond bool, ifTrue, ifFalse string) string {
	if cond {
		return ifTrue
	}
	return ifFalse
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
