package shell

import (
	"errors"
	"fmt"
	"testing"
)

func TestDetect_BashWhenAvailable(t *testing.T) {
	probe := func(cmd []string) (string, int, error) {
		return "/bin/bash\n", 0, nil
	}
	got, err := Detect(probe)
	if err != nil {
		t.Fatal(err)
	}
	if got != Bash {
		t.Errorf("got %q want bash", got)
	}
}

func TestDetect_FallsBackToShWhenBashAbsent(t *testing.T) {
	calls := 0
	probe := func(cmd []string) (string, int, error) {
		calls++
		if calls == 1 {
			// command -v bash — empty stdout, exit 0 (the `|| true` keeps exit 0)
			return "", 0, nil
		}
		// sh exit-0 probe
		return "", 0, nil
	}
	got, err := Detect(probe)
	if err != nil {
		t.Fatal(err)
	}
	if got != Sh {
		t.Errorf("got %q want sh", got)
	}
	if calls != 2 {
		t.Errorf("expected 2 probe calls, got %d", calls)
	}
}

func TestDetect_NoShellAtAll(t *testing.T) {
	probe := func(cmd []string) (string, int, error) {
		return "", 0, fmt.Errorf("connection refused")
	}
	_, err := Detect(probe)
	if !errors.Is(err, ErrNoShell) {
		t.Errorf("want ErrNoShell, got %v", err)
	}
}

func TestDetect_NoShellExitNonZero(t *testing.T) {
	probe := func(cmd []string) (string, int, error) {
		return "", 127, nil
	}
	_, err := Detect(probe)
	if !errors.Is(err, ErrNoShell) {
		t.Errorf("want ErrNoShell, got %v", err)
	}
}
