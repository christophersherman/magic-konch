# Magic Konch — your shell, in the pod.

A `kubectl` plugin that brings your local shell config into pods when you
exec in. Mechanism, not opinion.

- **Your aliases follow you into pods. Zero footprint.** No writes inside
  the container; works on read-only rootfs.
- **Bash history that survives pod restarts**, keyed by the controlling
  Deployment / StatefulSet / DaemonSet / CronJob — not by pod hash.
- **Prompt context for free.** `$KONCH_POD`, `$KONCH_NAMESPACE`,
  `$KONCH_WORKLOAD` are exported into the session — your rcfile decides
  what to do with them.
- **Plays nice with distroless and read-only rootfs.** Bash gets injected
  via process substitution, never via files in the pod.
- **`--probe` for safety.** A dry-run that tells you exactly what would
  happen — chosen container, resolved workload key, rcfile sources, the
  full command — without executing.
- **Per-context layered config.** `~/.config/konch/rc` always; plus
  `~/.config/konch/rc.<context>` when present.

> *All hail the Magic Konch.*

## Install

### Homebrew

```sh
brew install christophersherman/tap/konch
```

### `go install`

```sh
go install github.com/christophersherman/magic-konch/cmd/kubectl-konch@latest
```

### Krew

```sh
kubectl krew install konch
```

> Pending submission to [`kubernetes-sigs/krew-index`](https://github.com/kubernetes-sigs/krew-index).
> Until that merges you can install from the in-repo manifest:
>
> ```sh
> kubectl krew install --manifest=https://raw.githubusercontent.com/christophersherman/magic-konch/main/.krew.yaml
> ```

### Direct binary download

Pre-built binaries for linux/darwin × amd64/arm64 are attached to every
[GitHub release](https://github.com/christophersherman/magic-konch/releases).

`kubectl-konch` is the binary; `kubectl konch <pod>` is how you invoke it
(kubectl auto-discovers `kubectl-*` binaries on `$PATH`).

## Quick start

1. Drop your shell config at `~/.config/konch/rc`. This is just bash —
   aliases, exports, functions, prompt setup, whatever you want.

   ```sh
   # ~/.config/konch/rc
   alias k=kubectl
   alias gs='git status'
   export EDITOR=vim
   PS1='\[\e[1;33m\]konch:\[\e[0m\]$KONCH_NAMESPACE/$KONCH_WORKLOAD\$ '
   ```

2. Optionally add per-context overrides:

   ```sh
   # ~/.config/konch/rc.shermlabs
   alias kx='kubectl --context shermlabs'
   ```

3. Exec in:

   ```sh
   kubectl konch my-pod
   kubectl konch my-pod -c sidecar
   kubectl konch my-pod --probe         # dry-run, no exec
   ```

## What you get inside the pod

These are exported before bash sources your rcfile:

| Variable | Example |
|---|---|
| `KONCH_CONTEXT` | `shermlabs` |
| `KONCH_NAMESPACE` | `apps` |
| `KONCH_POD` | `csherman-net-8577fcf766-cnkwp` |
| `KONCH_CONTAINER` | `web` |
| `KONCH_WORKLOAD` | `Deployment/csherman-net` |
| `KONCH_WORKLOAD_KIND` | `Deployment` |
| `KONCH_WORKLOAD_NAME` | `csherman-net` |
| `HISTFILE` | `/tmp/.konch_history` (workload-keyed on the host) |

`TERM` is propagated from your local terminal when the pod doesn't have a
better one set, so `vim`, `less`, etc. render correctly.

## How history works

Konch reads your local file at
`~/.local/share/konch/history/<context>/<namespace>/<workload>`, ships it
into the pod via env-var on the exec command line, points `HISTFILE` at a
tmp path inside the pod, and on disconnect re-execs to fetch the updated
file back. The local copy is keyed by the controlling workload, so a fresh
pod from the same Deployment picks up where the last one left off.

Heavy users (>256 KB on disk) get tail-only uploads with the host-side
head preserved on writeback — see `internal/history/history.go`.

## `--probe`

```
$ kubectl konch -n git forgejo-754df645fc-28wsj --probe
Konch v0.1.0 dry-run — would exec into:
  context:    shermlabs
  namespace:  git
  pod:        forgejo-754df645fc-28wsj
  container:  forgejo  (only container in pod)

Resolved:
  shell:      bash
  workload:   Deployment/forgejo
  history:    ~/.local/share/konch/history/shermlabs/git/Deployment_forgejo (310 bytes)
  TERM:       xterm-256color (passed through if pod has none)

rcfile sources (merged in order, last wins):
  - ~/.config/konch/rc
  - ~/.config/konch/rc.shermlabs

Command that would be exec'd:
  $ /bin/bash -c '...'

~ the Konch has spoken ~
```

## Philosophy: mechanism, not opinion

Konch ships **no defaults**. No bundled aliases, no default prompt, no
opinion about your tools. It exposes:

- A way to source your config inside a pod.
- A few env-vars your config can read.
- A dry-run.

Everything else is *your* rcfile's job.

What Konch will **not** do:

- Default `grep`→`rg`, `ll`, etc. — you own your rcfile.
- Provide a fuzzy pod picker. ([k9s](https://k9scli.io),
  [kubectl-iexec](https://github.com/gabeduke/kubectl-iexec) already do.)
- Bundle ripgrep / bat / etc. — violates zero-footprint.
- AI features. Feature creep.
- Default prompts.

## Out of scope (for now)

These are intentionally deferred to a future version:

- `--copy-rcfile` for ash/dash environments (requires writing to `/tmp`)
- `--debug` wrapping `kubectl debug` for distroless containers
- `-E VAR1,VAR2` to pass through named local env vars
- Shell completion injection inside the pod
- zsh/fish support inside the pod (bash and sh only for v0.1)

## Edge cases

- **No bash, only sh:** Konch warns once on stderr, sources nothing
  (writing to `/tmp` for sh is v0.2 territory), still exports `KONCH_*`.
- **Distroless / no shell at all:** Clear error pointing you at
  `kubectl debug`.
- **Multi-container pod:** Honors `kubectl.kubernetes.io/default-container`
  annotation; otherwise asks for `-c` with a list of containers.
- **`Ctrl-P` collides with kubectl detach-keys:** Document workaround:
  set `KUBECTL_DETACH_KEYS` or use the `--detach-keys=...` flag (kubectl
  default is `Ctrl-P,Ctrl-Q`).

## See also

- [`xxh`](https://github.com/xxh/xxh) — same idea for SSH; heavier
  (uploads a portable shell into the remote).
- [`sshrc`](https://github.com/Russell91/sshrc),
  [`kyrat`](https://github.com/fsquillace/kyrat) — same idea for SSH;
  rely on `/tmp` writes.
- [`kubectl-iexec`](https://github.com/gabeduke/kubectl-iexec),
  [`kubectl-fuzzy`](https://github.com/d-kuro/kubectl-fuzzy) — pod
  pickers (complementary).
- [`kube-ps1`](https://github.com/jonmosco/kube-ps1) — local prompt for
  kubectl context (complementary).
- [`kuberc`](https://kubernetes.io/docs/reference/kubectl/kuberc/)
  (Kubernetes 1.33+) — client-side kubectl aliases. **Different layer**:
  Konch ships your shell into the pod; `kuberc` aliases `kubectl` itself.

## Build from source

```sh
git clone https://github.com/christophersherman/magic-konch
cd magic-konch
make build
./kubectl-konch --version
```

Tests:

```sh
make test
```

## License

Apache 2.0. See [LICENSE](LICENSE).
