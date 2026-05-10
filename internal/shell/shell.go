// Package shell detects which shell is available inside a target container.
//
// Bash is preferred (it supports `--rcfile <(...)` process substitution, which
// is how Konch injects without writing inside the pod). When bash is missing
// Konch falls back to /bin/sh and warns once that the rcfile won't be sourced.
// When neither exists Konch errors and points at `kubectl debug`.
package shell

import (
	"errors"
	"fmt"
	"strings"
)

// Shell is the resolved shell to invoke inside the pod.
type Shell string

// Bash and Sh are the two shells Detect can return. v0.1 supports nothing
// else inside the pod — zsh/fish are out of scope per CLAUDE.md.
const (
	Bash Shell = "bash"
	Sh   Shell = "sh"
)

// ProbeFunc runs cmd inside the target container and returns the trimmed
// stdout and the process exit code. err signals a transport-level failure
// (the apiserver could not reach the kubelet, etc.) — a non-zero exit is
// reported via the exit return, not as an error.
type ProbeFunc func(cmd []string) (stdout string, exit int, err error)

// ErrNoShell is returned when the container has neither bash nor sh.
var ErrNoShell = errors.New("no usable shell in container")

// Detect probes for bash; falls back to sh; errors with ErrNoShell when
// neither is present. The caller is expected to wrap with a `kubectl debug`
// suggestion that includes the actual pod name.
func Detect(probe ProbeFunc) (Shell, error) {
	out, exit, err := probe([]string{"/bin/sh", "-c", "command -v bash || true"})
	if err == nil && exit == 0 && strings.TrimSpace(out) != "" {
		return Bash, nil
	}
	_, exit, err = probe([]string{"/bin/sh", "-c", "exit 0"})
	if err == nil && exit == 0 {
		return Sh, nil
	}
	return "", fmt.Errorf("%w: container has neither /bin/bash nor /bin/sh", ErrNoShell)
}
