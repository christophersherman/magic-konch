// root.go defines `konch <pod>`: the default per-session exec flow.
// It resolves pod + container + workload, loads the user's shellrc (or,
// in v0.1, the konch rcfile — to be replaced in phase-A5), ships content
// into the pod via env vars, runs an interactive bash with the rcfile
// sourced via process substitution, and on disconnect fetches history
// back to the host. Mechanism, not opinion.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/client-go/kubernetes"

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

A quality-of-life tool that makes interactive pod exec feel like home.
After one-line setup, your aliases follow you into pods and bash history
is keyed by Deployment/StatefulSet/CronJob so it survives pod restarts.

  konch <pod>              # exec into <pod>, default container
  konch <pod> -c app       # pick a container
  konch <pod> --probe      # dry-run; report what would happen

The transparent kubectl-exec wrapper:
  eval "$(konch init bash)"  # in ~/.bashrc / ~/.zshrc
  # then  kubectl exec -it <pod> -- bash  routes through konch automatically

All hail the Magic Konch.`

func newRootCmd(streams genericiooptions.IOStreams) *cobra.Command {
	cf := genericclioptions.NewConfigFlags(true)
	var (
		container string
		probeOnly bool
	)
	cmd := &cobra.Command{
		Use:           "konch <pod>",
		Short:         "Bring your shell into a pod.",
		Long:          longHelp,
		Version:       version,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(cmd.Context(), streams, cf, args[0], container, probeOnly)
		},
	}
	cmd.Flags().StringVarP(&container, "container", "c", "",
		"container name (default: kubectl.kubernetes.io/default-container, else only container)")
	cmd.Flags().BoolVar(&probeOnly, "probe", false,
		"dry-run: report what Konch would do, without exec'ing")
	cf.AddFlags(cmd.Flags())

	cmd.AddCommand(
		newInitCmd(streams),
		newSetupCmd(streams),
		newHistoryCmd(streams),
		newConfigCmd(streams),
	)
	return cmd
}

func runExec(
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

	if sh == shell.Bash {
		if err := pullHistory(client, target, histPath, snap); err != nil {
			fmt.Fprintf(streams.ErrOut, "konch: warning: save history: %v\n", err)
		}
	}
	return runErr
}

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
	return []string{"/bin/sh", "-i"}
}

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

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

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
