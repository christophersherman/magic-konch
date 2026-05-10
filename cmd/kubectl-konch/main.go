// kubectl-konch is the entrypoint for `kubectl konch`. It does the minimum
// each invocation needs: resolve pod + container, walk to a workload key,
// load the local rcfile, ship it (and the history file) into the pod via
// env-vars on the exec command line, then run an interactive bash that
// sources the rcfile via process substitution. On disconnect, history is
// fetched back. Mechanism, not opinion.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/client-go/kubernetes"
	kexec "k8s.io/client-go/util/exec"

	konchexec "github.com/christophersherman/magic-konch/internal/exec"
	"github.com/christophersherman/magic-konch/internal/history"
	"github.com/christophersherman/magic-konch/internal/probe"
	"github.com/christophersherman/magic-konch/internal/rcfile"
	"github.com/christophersherman/magic-konch/internal/shell"
	"github.com/christophersherman/magic-konch/internal/workload"
)

// version is overwritten at build time via -ldflags '-X main.version=...'.
// goreleaser drives this from the git tag.
var version = "dev"

const longHelp = `Magic Konch — your shell, in the pod.

A kubectl plugin that brings your local shell config into pods when you
exec in. Your aliases follow you. Bash history survives pod restarts,
keyed by the controlling Deployment/StatefulSet, not by pod hash.

Konch ships no defaults. Whatever's in ~/.config/konch/rc is what runs.

  kubectl konch <pod>             # exec into <pod>, default container
  kubectl konch <pod> -c app      # pick a container
  kubectl konch <pod> --probe     # dry-run; report what would happen

Per-context overrides: ~/.config/konch/rc.<kubectl-context-name>.

All hail the Magic Konch.`

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

func newRootCmd(streams genericiooptions.IOStreams) *cobra.Command {
	cf := genericclioptions.NewConfigFlags(true)
	var (
		container string
		probeOnly bool
	)
	cmd := &cobra.Command{
		Use:           "kubectl-konch <pod>",
		Short:         "Bring your shell into a pod.",
		Long:          longHelp,
		Version:       version,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), streams, cf, args[0], container, probeOnly)
		},
	}
	cmd.Flags().StringVarP(&container, "container", "c", "",
		"container name (default: kubectl.kubernetes.io/default-container, else only container)")
	cmd.Flags().BoolVar(&probeOnly, "probe", false,
		"dry-run: report what Konch would do, without exec'ing")
	cf.AddFlags(cmd.Flags())
	return cmd
}

func run(
	ctx context.Context,
	streams genericiooptions.IOStreams,
	cf *genericclioptions.ConfigFlags,
	podName, containerFlag string,
	probeOnly bool,
) error {
	cfg, err := cf.ToRESTConfig()
	if err != nil {
		return fmt.Errorf("load kubeconfig: %w", err)
	}
	namespace, _, err := cf.ToRawKubeConfigLoader().Namespace()
	if err != nil {
		return fmt.Errorf("resolve namespace: %w", err)
	}
	contextName, err := resolveContextName(cf)
	if err != nil {
		return err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("build kubernetes client: %w", err)
	}

	pod, err := cs.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get pod %s/%s: %w", namespace, podName, err)
	}
	if err := guardPodPhase(pod); err != nil {
		return err
	}
	chosen, why, err := chooseContainer(pod, containerFlag)
	if err != nil {
		return err
	}

	target := konchexec.Target{Namespace: namespace, Pod: podName, Container: chosen}
	client := konchexec.NewClient(cs, cfg)

	probeFn := func(cmdLine []string) (string, int, error) {
		// Probe execs are short — give them their own modest deadline so a
		// stuck kubelet doesn't hang Konch start-up forever.
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return client.Probe(pctx, target, cmdLine)
	}
	sh, err := shell.Detect(probeFn)
	if err != nil {
		if errors.Is(err, shell.ErrNoShell) {
			return fmt.Errorf("%w. Try `kubectl debug -n %s %s --image=busybox` for an ephemeral debug container", err, namespace, podName)
		}
		return err
	}

	resolver := workload.New(cs)
	wkey, err := resolver.Resolve(ctx, pod)
	if err != nil {
		return fmt.Errorf("resolve workload: %w", err)
	}

	histPath, err := history.LocalPath(contextName, namespace, wkey.String())
	if err != nil {
		return fmt.Errorf("history path: %w", err)
	}
	snap, err := history.Read(histPath)
	if err != nil {
		// Read errors on history are non-fatal — start the session anyway.
		fmt.Fprintf(streams.ErrOut, "konch: warning: read history %s: %v\n", histPath, err)
		snap = history.Snapshot{}
	}

	rc, err := rcfile.Load(contextName)
	if err != nil {
		return err
	}
	var skippedShLines []int
	rcBytes := rc.Bytes
	if sh == shell.Sh && len(rcBytes) > 0 {
		rcBytes, skippedShLines = rcfile.SkipForSh(rcBytes)
	}

	localTERM := os.Getenv("TERM")
	envs := konchEnvs(contextName, namespace, podName, chosen, wkey, localTERM)
	finalCmd := buildCommand(sh, envs, rcBytes, snap.ToShip)

	if probeOnly {
		probe.Render(streams.Out, probe.Report{
			Version:         version,
			Context:         contextName,
			Namespace:       namespace,
			Pod:             podName,
			Container:       chosen,
			ContainerWhy:    why,
			Shell:           sh,
			Workload:        wkey,
			HistoryPath:     histPath,
			HistoryToShip:   len(snap.ToShip),
			HistoryFullSize: snap.FullSize,
			LocalTERM:       localTERM,
			RCSources:       rc.Sources,
			SkippedShLines:  skippedShLines,
			FinalCommand:    finalCmd,
		})
		return nil
	}

	if sh == shell.Sh {
		fmt.Fprintln(streams.ErrOut, "konch: bash not found, falling back to sh — your rcfile will not be sourced")
	} else if rc.Empty {
		fmt.Fprintln(streams.ErrOut, "konch: no rcfile found at ~/.config/konch/rc. Try asking again.")
	}

	runErr := client.Run(ctx, target, konchexec.RunOptions{
		Command: finalCmd,
		Streams: streams,
		TTY:     true,
	})

	// History fetch runs even after Ctrl-C / non-zero exit, on a fresh
	// deadline detached from the (possibly cancelled) parent context.
	if sh == shell.Bash {
		if err := pullHistory(client, target, histPath, snap); err != nil {
			fmt.Fprintf(streams.ErrOut, "konch: warning: save history: %v\n", err)
		}
	}
	return runErr
}

// guardPodPhase rejects pods that aren't Running with a clear, actionable
// message naming the actual phase.
func guardPodPhase(pod *corev1.Pod) error {
	switch pod.Status.Phase {
	case corev1.PodRunning:
		return nil
	case corev1.PodPending:
		return fmt.Errorf("pod %s/%s is Pending (not yet scheduled or starting). Wait, then retry — or run `kubectl describe pod -n %s %s` to see why",
			pod.Namespace, pod.Name, pod.Namespace, pod.Name)
	case corev1.PodSucceeded, corev1.PodFailed:
		return fmt.Errorf("pod %s/%s has terminated (phase=%s). Konch can only exec into a Running pod",
			pod.Namespace, pod.Name, pod.Status.Phase)
	default:
		return fmt.Errorf("pod %s/%s is in phase %s, not Running. Konch can only exec into a Running pod",
			pod.Namespace, pod.Name, pod.Status.Phase)
	}
}

// resolveContextName returns the kubectl context name actually in effect
// (--context flag if set, else the kubeconfig's current-context).
func resolveContextName(cf *genericclioptions.ConfigFlags) (string, error) {
	if cf.Context != nil && *cf.Context != "" {
		return *cf.Context, nil
	}
	raw, err := cf.ToRawKubeConfigLoader().RawConfig()
	if err != nil {
		return "", fmt.Errorf("load raw kubeconfig: %w", err)
	}
	return raw.CurrentContext, nil
}

func chooseContainer(pod *corev1.Pod, flag string) (name, why string, err error) {
	if flag != "" {
		for _, c := range pod.Spec.Containers {
			if c.Name == flag {
				return flag, "--container flag", nil
			}
		}
		return "", "", fmt.Errorf("container %q not found in pod %s/%s; have: %s",
			flag, pod.Namespace, pod.Name, containerNames(pod))
	}
	if v, ok := pod.Annotations["kubectl.kubernetes.io/default-container"]; ok && v != "" {
		for _, c := range pod.Spec.Containers {
			if c.Name == v {
				return v, "kubectl.kubernetes.io/default-container annotation", nil
			}
		}
	}
	if len(pod.Spec.Containers) == 1 {
		return pod.Spec.Containers[0].Name, "only container in pod", nil
	}
	return "", "", fmt.Errorf("pod %s/%s has multiple containers and no default-container annotation; pick one with -c. have: %s",
		pod.Namespace, pod.Name, containerNames(pod))
}

func containerNames(pod *corev1.Pod) string {
	names := make([]string, len(pod.Spec.Containers))
	for i, c := range pod.Spec.Containers {
		names[i] = c.Name
	}
	return strings.Join(names, ", ")
}

func konchEnvs(contextName, namespace, pod, container string, wkey workload.Key, localTERM string) []envVar {
	envs := []envVar{
		{"KONCH_CONTEXT", contextName},
		{"KONCH_NAMESPACE", namespace},
		{"KONCH_POD", pod},
		{"KONCH_CONTAINER", container},
		{"KONCH_WORKLOAD", wkey.String()},
		{"KONCH_WORKLOAD_KIND", wkey.Kind},
		{"KONCH_WORKLOAD_NAME", wkey.Name},
	}
	if localTERM != "" {
		envs = append(envs, envVar{"KONCH_LOCAL_TERM", localTERM})
	}
	return envs
}

type envVar struct{ K, V string }

// buildCommand renders the exec argv for the chosen shell. The bash path
// uses process substitution to source a base64-encoded rcfile without
// writing inside the pod. The sh path skips rcfile injection (writing /tmp
// fallback is v0.2 territory) but still propagates the KONCH_* env vars.
func buildCommand(sh shell.Shell, envs []envVar, rcBytes, histBytes []byte) []string {
	exports := make([]string, 0, len(envs)+2)
	for _, e := range envs {
		exports = append(exports, fmt.Sprintf("%s=%s", e.K, shellSingleQuote(e.V)))
	}

	switch sh {
	case shell.Bash:
		fullRc := konchPrologue()
		if len(rcBytes) > 0 {
			if fullRc[len(fullRc)-1] != '\n' {
				fullRc = append(fullRc, '\n')
			}
			fullRc = append(fullRc, rcBytes...)
		}
		exports = append(exports,
			"KONCH_RC_B64="+shellSingleQuote(base64.StdEncoding.EncodeToString(fullRc)))
		if len(histBytes) > 0 {
			exports = append(exports,
				"KONCH_HIST_B64="+shellSingleQuote(base64.StdEncoding.EncodeToString(histBytes)))
		}
		inner := "export " + strings.Join(exports, " ") +
			`; exec bash --rcfile <(printf '%s' "$KONCH_RC_B64" | base64 -d) -i`
		return []string{"/bin/bash", "-c", inner}

	case shell.Sh:
		inner := "export " + strings.Join(exports, " ") + "; exec sh -i"
		return []string{"/bin/sh", "-c", inner}
	}
	// Should be unreachable; Detect returns ErrNoShell otherwise.
	return []string{"/bin/sh", "-i"}
}

// konchPrologue is the rcfile fragment Konch prepends to the user's content.
// Only history wiring, a TERM default, and a baseline HISTFILE — no aliases,
// no prompt, no opinion. The user's rcfile may override anything here.
func konchPrologue() []byte {
	return []byte(`# --- Konch prologue ---
if { [ -z "${TERM-}" ] || [ "$TERM" = "dumb" ]; } && [ -n "${KONCH_LOCAL_TERM-}" ]; then
  export TERM="$KONCH_LOCAL_TERM"
fi
unset KONCH_LOCAL_TERM
if [ -n "${KONCH_HIST_B64-}" ]; then
  printf '%s' "$KONCH_HIST_B64" | base64 -d > ` + history.PodPath + ` 2>/dev/null || true
  unset KONCH_HIST_B64
fi
export HISTFILE=` + history.PodPath + `
export HISTSIZE=10000 HISTFILESIZE=10000
shopt -s histappend 2>/dev/null
PROMPT_COMMAND="history -a${PROMPT_COMMAND:+; $PROMPT_COMMAND}"
# --- end Konch prologue ---
`)
}

// shellSingleQuote single-quotes s for safe inclusion in a `sh -c` script.
// Single-quote inside the string is closed-escaped-reopened: '\”.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// pullHistory reads the in-pod HISTFILE and merges it back to the host file.
// Best-effort: a destroyed pod, missing base64, or read-only /tmp all degrade
// to silent no-ops rather than failing the command. Uses history.WriteMerge
// so any head bytes we couldn't ship at session start are preserved.
func pullHistory(client *konchexec.Client, t konchexec.Target, localPath string, snap history.Snapshot) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, exit, err := client.Probe(ctx, t,
		[]string{"/bin/sh", "-c", "base64 < " + history.PodPath + " 2>/dev/null"})
	if err != nil {
		return err
	}
	if exit != 0 || strings.TrimSpace(out) == "" {
		return nil
	}
	cleaned := bytes.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, []byte(out))
	data, err := base64.StdEncoding.DecodeString(string(cleaned))
	if err != nil {
		return fmt.Errorf("decode history: %w", err)
	}
	return history.WriteMerge(localPath, snap, data)
}
