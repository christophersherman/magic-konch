// Package history persists bash history per workload, on the host, so it
// survives pod restarts. v0.1 strategy: ship the existing history to the pod
// via env on session start, set HISTFILE inside the pod to a tmp path, and
// re-exec on disconnect to copy the updated file back.
package history

import (
	"os"
	"path/filepath"
	"strings"
)

// PodPath is where Konch tells bash to write history inside the pod.
//
// /tmp is writable on the overwhelming majority of containers (including
// distroless-shell variants and read-only-rootfs pods that mount /tmp as
// emptyDir). If a pod has /tmp read-only the history feature degrades
// silently — the rest of the rcfile injection still works.
const PodPath = "/tmp/.konch_history"

// MaxBytesUploaded caps the size of the on-disk history we ship into the
// pod via env-var. Above this, we tail to the most recent bytes. K8s
// exec command-strings flow through the apiserver URL, so unbounded growth
// would eventually break the request.
const MaxBytesUploaded = 256 * 1024

// LocalPath returns the host-side history file path for the given context,
// namespace, and workload key. Uses XDG_DATA_HOME when set, else
// ~/.local/share/konch/history.
func LocalPath(kctx, namespace, workload string) (string, error) {
	base, err := dataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "history",
		safeSegment(kctx, "_no-context_"),
		safeSegment(namespace, "_no-namespace_"),
		safeSegment(workload, "_no-workload_"),
	), nil
}

func dataDir() (string, error) {
	if d := os.Getenv("KONCH_DATA_DIR"); d != "" {
		return d, nil
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "konch"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "konch"), nil
}

// safeSegment replaces path separators in a segment so workload names like
// "Deployment/csherman-net" can't escape the history directory. Empty input
// becomes the supplied placeholder.
func safeSegment(s, empty string) string {
	if s == "" {
		return empty
	}
	r := strings.NewReplacer("/", "_", "\x00", "_", "..", "_")
	return r.Replace(s)
}

// Read returns the existing history bytes at path, capped to the most recent
// MaxBytesUploaded. Missing files return (nil, nil) — the caller should ship
// no env var and let bash start with an empty HISTFILE.
func Read(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(b) > MaxBytesUploaded {
		// Trim to a line boundary so we don't ship a half-line at the head.
		tail := b[len(b)-MaxBytesUploaded:]
		if i := indexOfNewline(tail); i >= 0 && i+1 < len(tail) {
			tail = tail[i+1:]
		}
		return tail, nil
	}
	return b, nil
}

func indexOfNewline(b []byte) int {
	for i, c := range b {
		if c == '\n' {
			return i
		}
	}
	return -1
}

// Write atomically writes the history bytes back to path, creating parent
// directories as needed. Permissions match a typical bash_history file (0600).
func Write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".konch-hist-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
