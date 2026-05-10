// Package konchexec wraps client-go's remotecommand for the two flavours of
// pod exec Konch needs: a buffered Probe (for shell detection and history
// fetch) and an interactive Run with TTY raw mode and SIGWINCH passthrough.
package konchexec

import (
	"bytes"
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/util/exec"
	"k8s.io/kubectl/pkg/util/term"
)

// Target identifies a single container exec target.
type Target struct {
	Namespace string
	Pod       string
	Container string
}

// Client issues exec requests against a single kubeconfig.
type Client struct {
	cs  kubernetes.Interface
	cfg *restclient.Config
}

// NewClient builds a Client. Both the typed kubernetes.Interface and the raw
// REST config are required: the typed client builds the request URL, the raw
// config configures the SPDY transport.
func NewClient(cs kubernetes.Interface, cfg *restclient.Config) *Client {
	return &Client{cs: cs, cfg: cfg}
}

// Probe runs cmd in the target with no TTY and returns the captured stdout
// and the process exit code. A non-zero exit is reflected in the int, not as
// an error; transport failures are returned as the error.
func (c *Client) Probe(ctx context.Context, t Target, cmd []string) (string, int, error) {
	req := c.cs.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(t.Namespace).
		Name(t.Pod).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: t.Container,
			Command:   cmd,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)
	executor, err := remotecommand.NewSPDYExecutor(c.cfg, "POST", req.URL())
	if err != nil {
		return "", 0, fmt.Errorf("build executor: %w", err)
	}
	var stdout, stderr bytes.Buffer
	streamErr := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if streamErr != nil {
		var ce exec.CodeExitError
		if errorsAs(streamErr, &ce) {
			return stdout.String(), ce.Code, nil
		}
		return stdout.String(), 0, fmt.Errorf("probe exec: %w", streamErr)
	}
	return stdout.String(), 0, nil
}

// RunOptions controls an interactive exec session.
type RunOptions struct {
	Command []string
	Streams genericiooptions.IOStreams
	TTY     bool
}

// Run launches an interactive exec session, attaches the local terminal in
// raw mode (when TTY is true and stdin is a terminal), and forwards SIGWINCH
// resize events. Returns the remote process exit code via *exec.CodeExitError
// inside the returned error when the command exits non-zero.
func (c *Client) Run(ctx context.Context, t Target, opts RunOptions) error {
	tt := term.TTY{
		In:     opts.Streams.In,
		Out:    opts.Streams.Out,
		Raw:    opts.TTY,
		TryDev: true,
	}
	useTTY := opts.TTY && tt.IsTerminalIn()

	req := c.cs.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(t.Namespace).
		Name(t.Pod).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: t.Container,
			Command:   opts.Command,
			Stdin:     true,
			Stdout:    true,
			Stderr:    !useTTY, // with TTY, stderr is multiplexed onto stdout
			TTY:       useTTY,
		}, scheme.ParameterCodec)
	executor, err := remotecommand.NewSPDYExecutor(c.cfg, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("build executor: %w", err)
	}

	var sizeQueue remotecommand.TerminalSizeQueue
	if useTTY {
		if q := tt.MonitorSize(tt.GetSize()); q != nil {
			sizeQueue = sizeQueueAdapter{q: q}
		}
	}

	stream := func() error {
		so := remotecommand.StreamOptions{
			Stdin:             opts.Streams.In,
			Stdout:            opts.Streams.Out,
			Tty:               useTTY,
			TerminalSizeQueue: sizeQueue,
		}
		if !useTTY {
			so.Stderr = opts.Streams.ErrOut
		}
		return executor.StreamWithContext(ctx, so)
	}
	if useTTY {
		return tt.Safe(stream)
	}
	return stream()
}

// errorsAs is a thin wrapper so we don't have to import "errors" twice.
func errorsAs(err error, target any) bool { return errorsAsImpl(err, target) }

// sizeQueueAdapter bridges k8s.io/kubectl/pkg/util/term.TerminalSizeQueue
// (which returns *term.TerminalSize) to client-go's remotecommand contract
// (which wants *remotecommand.TerminalSize). The two types are identical in
// layout; this adapter just rewraps the field values.
type sizeQueueAdapter struct{ q term.TerminalSizeQueue }

func (a sizeQueueAdapter) Next() *remotecommand.TerminalSize {
	s := a.q.Next()
	if s == nil {
		return nil
	}
	return &remotecommand.TerminalSize{Width: s.Width, Height: s.Height}
}
