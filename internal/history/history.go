// Package history persists bash history per workload, on the host, so it
// survives pod restarts. v0.1 strategy: ship the existing history into the
// pod via env on session start, set HISTFILE there to a tmp path, and
// re-exec on disconnect to copy the updated file back.
//
// Heavy users may have on-disk history larger than what Konch is willing to
// ship over the apiserver URL (MaxBytesUploaded). In that case Read returns
// only the tail, and WriteMerge stitches the new bytes from the pod onto the
// untouched head when it can prove they fit cleanly.
package history

import (
	"bytes"
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
// pod via env-var. Above this, Read returns only the most-recent tail.
// K8s exec command-strings flow through the apiserver URL, so unbounded
// growth would eventually break the request.
const MaxBytesUploaded = 256 * 1024

// Snapshot is the result of reading the local history file. ToShip is the
// payload to upload (capped to MaxBytesUploaded, trimmed to a line
// boundary). FullSize records the size of the on-disk file before capping
// so WriteMerge can preserve any head we couldn't ship.
type Snapshot struct {
	ToShip   []byte
	FullSize int64
}

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

// Read returns a Snapshot. Missing files yield an empty Snapshot with no
// error — the caller should ship no env var and let bash start fresh.
func Read(path string) (Snapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Snapshot{}, nil
		}
		return Snapshot{}, err
	}
	full := int64(len(b))
	if int64(len(b)) > MaxBytesUploaded {
		// Trim to a line boundary so we don't ship a half-line at the head.
		tail := b[len(b)-MaxBytesUploaded:]
		if i := bytes.IndexByte(tail, '\n'); i >= 0 && i+1 < len(tail) {
			tail = tail[i+1:]
		}
		return Snapshot{ToShip: tail, FullSize: full}, nil
	}
	return Snapshot{ToShip: b, FullSize: full}, nil
}

// Write atomically writes the history bytes back to path, creating parent
// directories as needed. Permissions match a typical bash_history file
// (0600). Used internally by WriteMerge and exposed for tests.
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

// WriteMerge writes podBytes back to path, preserving any head bytes we
// trimmed at upload time. Three branches:
//
//  1. The local file fit entirely into snap.ToShip — overwrite with podBytes.
//  2. We trimmed a head, AND podBytes starts with snap.ToShip — append
//     only the new suffix to the existing local file (true merge).
//  3. We trimmed a head, but podBytes does NOT start with snap.ToShip
//     (bash truncated to HISTFILESIZE mid-session, or HISTCONTROL rewrote
//     it) — fall back to overwrite. We lose the trimmed head; bash's view
//     wins. Pragmatic v0.1 trade-off; v0.2 could do tail-suffix matching.
func WriteMerge(path string, snap Snapshot, podBytes []byte) error {
	if snap.FullSize <= int64(len(snap.ToShip)) {
		return Write(path, podBytes)
	}
	if bytes.HasPrefix(podBytes, snap.ToShip) {
		newSuffix := podBytes[len(snap.ToShip):]
		existing, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return Write(path, podBytes)
			}
			return err
		}
		merged := make([]byte, 0, len(existing)+len(newSuffix))
		merged = append(merged, existing...)
		merged = append(merged, newSuffix...)
		return Write(path, merged)
	}
	return Write(path, podBytes)
}
