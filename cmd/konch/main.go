// konch is the Magic Konch CLI: bring your shell into a pod.
//
// Default action (`konch <pod>`) opens an interactive bash session inside
// the named pod with the user's local shellrc aliases injected, KONCH_*
// env vars exported, and bash history persisted on the host keyed by the
// pod's controlling workload.
//
// Subcommands (init / setup / history / config) handle the rest of the
// quality-of-life surface — transparent kubectl interception, k9s
// integration, local fzf over history, and config inspection.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/cli-runtime/pkg/genericiooptions"
	kexec "k8s.io/client-go/util/exec"
)

func main() {
	streams := genericiooptions.IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := newRootCmd(streams)
	if err := cmd.ExecuteContext(ctx); err != nil {
		var ce kexec.CodeExitError
		if errors.As(err, &ce) {
			os.Exit(ce.Code)
		}
		fmt.Fprintln(streams.ErrOut, "konch:", err)
		os.Exit(1)
	}
}
