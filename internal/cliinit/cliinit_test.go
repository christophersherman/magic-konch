package cliinit

import (
	"os/exec"
	"strings"
	"testing"
)

func TestBash_SnippetShape(t *testing.T) {
	s := Bash()
	for _, want := range []string{
		"kubectl()",
		"__konch_detect",
		"command konch",
		"command kubectl",
		"__konch_saw_exec=1",
		"--container",
		"--namespace",
		"--context",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("snippet missing %q", want)
		}
	}
}

func TestZsh_ReusesPosixBody(t *testing.T) {
	if Zsh() == "" {
		t.Fatal("Zsh() returned empty")
	}
	// Body should match Bash() except for the comment header.
	if Zsh() == Bash() {
		t.Error("Bash() and Zsh() should differ at least in their headers")
	}
	// The function itself should be identical.
	for _, want := range []string{"kubectl()", "__konch_detect", "command konch", "command kubectl"} {
		if !strings.Contains(Zsh(), want) {
			t.Errorf("zsh snippet missing %q", want)
		}
	}
}

func TestZsh_ParsesUnderRealZsh(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not on PATH; skipping syntax check")
	}
	cmd := exec.Command("zsh", "-n")
	cmd.Stdin = strings.NewReader(Zsh())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("zsh -n rejected snippet:\n%s\n--- snippet ---\n%s", out, Zsh())
	}
}

// TestBash_ParsesUnderRealBash exercises the snippet by sourcing it in
// `bash -n` (no-exec syntax check). If bash isn't available, the test
// skips rather than failing — CI containers may not have bash.
func TestBash_ParsesUnderRealBash(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH; skipping syntax check")
	}
	cmd := exec.Command("bash", "-n")
	cmd.Stdin = strings.NewReader(Bash())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash -n rejected snippet:\n%s\n--- snippet ---\n%s", out, Bash())
	}
}

// TestBash_InterceptShape sources the snippet, calls kubectl with a
// shape that should be intercepted, and verifies konch (stubbed) was
// invoked with the right args.
func TestBash_InterceptShape(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	// Run a bash subprocess that:
	// 1. Sources our snippet.
	// 2. Stubs `command kubectl` and `command konch` to print their args.
	// 3. Invokes kubectl exec -it foo -- bash.
	//
	// `command` is a bash builtin that bypasses functions. We can't easily
	// stub `command` itself, so instead we put a PATH-shadow konch + kubectl
	// in a tempdir and prepend it to PATH.
	script := `
set -e
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
cat > "$TMP/konch" <<'EOF'
#!/bin/sh
echo "konch_called args=$*"
EOF
cat > "$TMP/kubectl" <<'EOF'
#!/bin/sh
echo "kubectl_called args=$*"
EOF
chmod +x "$TMP/konch" "$TMP/kubectl"
export PATH="$TMP:$PATH"

eval "$KONCH_SNIPPET"

# Should intercept (post-doubledash is bash):
kubectl exec -it -n git forgejo-xyz -- bash

# Should pass through (post-doubledash is non-shell command):
kubectl exec -it forgejo-xyz -- ls /tmp

# Should pass through (not exec at all):
kubectl get pods
`
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(cmd.Env, "PATH=/usr/bin:/bin", "KONCH_SNIPPET="+Bash())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "konch_called args=forgejo-xyz --namespace git") {
		t.Errorf("expected konch interception for `kubectl exec ... -- bash`, got:\n%s", got)
	}
	if !strings.Contains(got, "kubectl_called args=exec -it forgejo-xyz -- ls /tmp") {
		t.Errorf("expected passthrough for `kubectl exec ... -- ls /tmp`, got:\n%s", got)
	}
	if !strings.Contains(got, "kubectl_called args=get pods") {
		t.Errorf("expected passthrough for `kubectl get pods`, got:\n%s", got)
	}
}
