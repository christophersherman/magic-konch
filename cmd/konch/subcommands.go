// subcommands.go holds stubs for the v0.2 surface that isn't yet built:
// `konch init <shell>`, `konch setup <tool>`, `konch history [<workload>]`,
// and `konch config show|edit`. Each command's implementation lands in a
// later phase; these stubs ensure the cobra surface is in place so the
// help text, --version, and subcommand discovery work today.
package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/christophersherman/magic-konch/internal/cliinit"
)

var errNotYetImplemented = errors.New("not yet implemented in this v0.2 build — see CLAUDE.md for the roadmap")

func newInitCmd(streams genericiooptions.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init <shell>",
		Short: "Print a shell function that intercepts kubectl exec.",
		Long: `Emit a shell function that wraps kubectl. After ` + "`eval \"$(konch init bash)\"`" + `
in your shellrc, typing ` + "`kubectl exec -it <pod> -- bash`" + ` routes
through konch transparently — your shellrc aliases follow you in, history
persists by workload, KONCH_* env vars are set.

Supported shells: bash (zsh and fish coming in v0.2.x).`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				_, err := fmt.Fprint(streams.Out, cliinit.Bash())
				return err
			case "zsh":
				_, err := fmt.Fprint(streams.Out, cliinit.Zsh())
				return err
			case "fish":
				return fmt.Errorf("shell %q is coming in v0.2.x — bash and zsh only for now", args[0])
			default:
				return fmt.Errorf("unsupported shell %q (try: bash, zsh)", args[0])
			}
		},
	}
	return cmd
}

func newSetupCmd(streams genericiooptions.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup <tool>",
		Short: "Configure a tool to route its pod-exec sessions through konch.",
		Long: `Wire a third-party tool's exec path to konch. Currently planned:

  konch setup k9s    Add a k9s plugin entry so the shell shortcut goes via konch.

OpenLens / Lens / VS Code k8s extension transparency is out of scope
for v0.2 — those tools call the Kubernetes API directly with no local
hook. Use ` + "`konch <pod>`" + ` explicitly in those.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			_ = streams
			_ = args
			return errNotYetImplemented
		},
	}
	return cmd
}

func newHistoryCmd(streams genericiooptions.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history [<workload>]",
		Short: "Local fuzzy search over persisted per-workload history.",
		Long: `Search the local history files that konch maintains per workload at
~/.local/share/konch/history/<context>/<namespace>/<workload>.

Without an argument, picks a workload via fzf, then fuzzy-searches its
history. With ` + "`<workload>`" + ` (matched against any history file's path),
searches that workload directly. Never touches the pod.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			_ = streams
			_ = args
			return errNotYetImplemented
		},
	}
	return cmd
}

func newConfigCmd(streams genericiooptions.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect or edit ~/.config/konch/config.toml.",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "show",
			Short: "Print the current resolved config (defaults + overrides).",
			RunE: func(_ *cobra.Command, _ []string) error {
				_ = streams
				return errNotYetImplemented
			},
		},
		&cobra.Command{
			Use:   "edit",
			Short: "Open ~/.config/konch/config.toml in $EDITOR.",
			RunE: func(_ *cobra.Command, _ []string) error {
				_ = streams
				return errNotYetImplemented
			},
		},
	)
	return cmd
}
