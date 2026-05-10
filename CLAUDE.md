# CLAUDE.md — Magic Konch (`konch`)

## What you're building

A local quality-of-life tool that makes every interactive pod exec feel like home. The project is **Magic Konch**, the CLI is **`konch`**, distributed as a single static binary.

**Headline UX:**

```sh
brew install christophersherman/tap/konch
eval "$(konch init bash)"   # add to .bashrc / .zshrc / config.fish, once
konch setup k9s             # one-time
# ...then forget konch exists. `kubectl exec -it <pod> -- bash` and the
# `s` shortcut in k9s both go through konch. From the user's point of
# view, every interactive exec session now has:
# - Their existing shell aliases (from .bashrc / .zshrc / config.fish).
# - Bash history keyed by Deployment / StatefulSet / CronJob, persisted
#   on the user's Mac. Survives pod death, helm upgrades, scaling events.
# - Prompt-context env vars ($KONCH_POD, $KONCH_NAMESPACE, $KONCH_WORKLOAD).
```

**No daemon, no IPC, no persistent process.** State is files on disk under `~/.local/share/konch/` and `~/.config/konch/`. Konch is invoked per session (transparently via the shell wrapper, or explicitly via `konch <pod>`), reads/writes those files at session start and end, exits. Between sessions, no konch process exists.

**Intended scale:** thousands of users install it once and never touch the config again.

## Name & the bit

The project name is a SpongeBob reference (the Magic Conch episode), with the `K` doing double duty for Kubernetes and for the conch *shell* — which is what we bring into the pod. The bit is part of the product, not decoration. It is intentionally on-brand for the ecosystem (stern, popeye, k9s, ksniff).

**Voice and tone:**
- Light touch, never overdone. The bit lands once or twice per surface, not in every sentence.
- The Konch is *passive and a little unhelpful*, like in the show. That voice fits a "mechanism not opinion" tool — the Konch doesn't tell you what to do, it just relays what's already true.
- Examples that work: `--probe` ending with `~ the Konch has spoken ~`, an empty-aliases case that says `Nothing to bring.`, a workload-not-found case with `Nothing.`. One per surface, no more.
- All error messages must still be actionable first, charming second. "bash not found in pod, falling back to sh" is the message; the personality goes in the help text and probe output, not in error paths the user needs to debug.

## Philosophy: mechanism, with one opinion

Konch is mostly mechanism: it reads the user's existing shellrc, injects the relevant bits into the pod, persists history per workload, exposes env vars, gets out of the way.

**The one opinion:** *konch should be the default for interactive pod exec.* The user opts into this opinion by `eval`-ing `konch init <shell>` in their shellrc and running `konch setup k9s` once. Nothing happens without that explicit opt-in.

Beyond that one opinion, mechanism-not-opinion still holds. **Reject these features no matter how tempting:**

- Default aliases (`grep`→`rg`, `ll`, etc.) — read what the user already has.
- fzf / interactive pod picker — k9s and `kubectl-iexec` already do this. (Local fzf over *history* is fine; that's not a picker.)
- Opinionated default prompt — expose env vars instead.
- Bundled tools (ripgrep, bat, etc.) inside the pod — violates zero-footprint.
- AI features, "explain this pod" — feature creep.
- A konch-specific rcfile separate from the user's shellrc — duplicate config is friction.

## v0.2 feature set

1. **Transparent `kubectl exec` interception** via shell-function wrapper. `konch init bash|zsh|fish` emits a function that wraps `kubectl`. When the user types `kubectl exec -it <pod> [-c <c>] -- <shell>`, the wrapper rewrites to `konch <pod> [-c <c>]`. Everything else passes through.
2. **k9s integration** via `konch setup k9s`. Exact hook TBD (k9s plugin shortcut vs. a `kubectl`-wrapper binary on PATH — pick the cleanest once we inspect k9s's config).
3. **Aliases sourced from real shellrc files.** Auto-detect from `$SHELL`: bash reads `~/.bashrc` (+ `~/.bash_profile`); zsh reads `~/.zshrc` (+ `~/.zshenv`); fish reads `~/.config/fish/config.fish`. Override paths via config.
4. **Opportunistic alias fallback.** For each `alias X=Y`, the in-pod prologue probes `command -v <first-word-of-Y>`; if missing, `unalias X` so the system X still works. Toggle in config.
5. **Workload-keyed bash history**, persisted at `~/.local/share/konch/history/<context>/<namespace>/<workload>`. Survives pod restarts, helm upgrades, scaling events. Mechanism unchanged from v0.1.
6. **`konch history [<workload>]`** — local fzf search across the persisted history files. Never touches the pod.
7. **Simple settings at `~/.config/konch/config.toml`.** Sensible defaults; users shouldn't need to edit it in the common case.
8. **Prompt-context env vars** — `KONCH_POD`, `KONCH_NAMESPACE`, `KONCH_CONTEXT`, `KONCH_WORKLOAD`, `KONCH_CONTAINER`.
9. **`--probe` dry-run** — enhanced to show which aliases would inject, which would fall back, and which integrations are active.
10. **Explicit `konch <pod>` fallback** for tools that can't be intercepted (OpenLens, scripts, ad-hoc). `kubectl konch <pod>` continues to work via a symlink.

## Architecture

- Language: Go. Single static binary on disk: `konch`. Installer symlinks `kubectl-konch → konch` so `kubectl konch` keeps working.
- Pattern: same as v0.1 — `k8s.io/cli-runtime` for kubeconfig, `k8s.io/client-go` for the workload walk and `pods/exec` stream, `github.com/spf13/cobra` for the subcommand tree.
- Config: TOML via `github.com/BurntSushi/toml` (stdlib-clean, small).
- Distribution: Homebrew tap (`christophersherman/homebrew-tap`); Krew manifest at `.krew.yaml`; goreleaser drives both.

**Project layout (post-v0.2):**

```
cmd/konch/main.go               # entrypoint, cobra root, subcommand wiring
internal/cliinit/               # `konch init <shell>` — emit shell wrappers
internal/setup/                 # `konch setup k9s` — write k9s config
internal/shellparse/            # extract aliases/exports from bashrc/zshrc/config.fish
internal/config/                # ~/.config/konch/config.toml load + defaults
internal/exec/                  # remotecommand wiring (unchanged from v0.1)
internal/workload/              # ownerReferences walk (unchanged)
internal/history/               # workload-keyed local persistence (unchanged)
internal/historysearch/         # local fzf over history files
internal/probe/                 # --probe report
internal/shell/                 # in-pod shell detection (unchanged)
internal/podsession/            # the per-exec flow: build command, run, fetch history
go.mod
.krew.yaml
.goreleaser.yaml
README.md
LICENSE
```

## Key technical details

### Persistence model

No daemon. State = files on disk. Konch is invoked per session (transparently or explicitly), reads `~/.local/share/konch/history/<context>/<ns>/<workload>` and the user's shellrc, ships content into the pod via env vars on the exec command line, pulls the updated history file back at session end via a second `pods/exec` probe. Between sessions, no konch process exists.

### How history flows (unchanged from v0.1)

1. Konch reads the local history file for the workload key (e.g. `shermlabs/minecraft/StatefulSet_minecraft`).
2. Base64-encodes it, attaches as `KONCH_HIST_B64` env on the exec command line.
3. The in-pod prologue decodes it to `/tmp/.konch_history`, sets `HISTFILE` to that, sets `PROMPT_COMMAND="history -a"` so every prompt flushes.
4. The user types commands; bash appends each to `/tmp/.konch_history` immediately.
5. On exit, konch re-execs into the pod, `base64 < /tmp/.konch_history`, decodes locally, merges with the on-disk file (preserving any head bytes that exceeded the upload cap), writes atomically.

The keying is on the controlling workload, *not* the pod name — so when a pod gets replaced (helm upgrade, scaling, eviction), the same history follows the new pod.

### Shell-function wrapper

`konch init <shell>` emits a function the user's shell sources via `eval "$(...)"`. The function aliases `kubectl` and parses argv:

- If the invocation matches `kubectl [<global flags>] exec [-it|--tty|...] <pod> [-c|--container <c>] [-n|--namespace <ns>] -- <shell-name>`, rewrite to `konch <pod> --container <c> --namespace <ns> [--context <ctx>]`.
- The post-`--` "command" must be a bare shell name (`bash`, `sh`, `zsh`, `fish`) or empty for the rewrite to fire. `kubectl exec pod -- ls /tmp` is a non-interactive one-off; pass it through to real kubectl.
- Anything else (`kubectl get`, `kubectl logs`, `kubectl apply`, etc.) passes through via `command kubectl "$@"`.

### Shellrc parsing

Per-shell parsers in `internal/shellparse/` that extract:

- **Aliases.** bash/zsh: `alias NAME='VALUE'` (POSIX quoting). fish: `alias NAME 'VALUE'` and `alias NAME VALUE`.
- **Simple exports.** `export FOO=bar` (bash/zsh), `set -gx FOO bar` (fish). Skip `export FOO=$(...)`, `${FOO:-default}` substitutions, etc. — anything that requires running shell code to resolve.

Deliberately *not* translated:
- zsh-specific function constructs (`autoload`, `compdef`, prompt machinery).
- fish-specific abbreviations (`abbr`).
- Complex prompt setups (PS1 vs. fish_prompt vs. PROMPT).

Skipped constructs emit one stderr line per session: `konch: skipped 3 zsh-specific lines (use --probe for details)`.

### Opportunistic alias fallback

The in-pod prologue, after applying aliases, runs:

```sh
for a in $KONCH_ALIASES; do
  name="${a%%=*}"; cmd="${a#*=}"; first="${cmd%% *}"
  command -v "$first" >/dev/null 2>&1 || unalias "$name" 2>/dev/null
done
```

`KONCH_ALIASES` is shipped in via env, list of `name=cmd` entries. Toggleable via `[aliases] opportunistic = false`.

### Configuration file

`~/.config/konch/config.toml`. Konch creates it with defaults on first run. Schema:

```toml
[interception]
kubectl = true   # the shell-function wrapper rewrites kubectl exec
k9s     = true   # k9s integration is active

[shell]
# Empty list = auto-detect from $SHELL.
# Override: a list of absolute paths to source aliases/exports from.
sources = []

[aliases]
opportunistic = true   # drop aliases whose target isn't in the pod

[history]
max_upload_bytes = 262144   # cap per-session env-var size (apiserver URL limit)
fzf              = "fzf"     # set to "" to disable `konch history`
```

## Edge cases

- **Alpine / dash / ash inside the pod**: no `bash --rcfile <(...)`. Detect bash absence; emit one stderr warning; skip rcfile injection and exec plain sh. KONCH_* env vars still propagate.
- **Distroless with no shell at all**: clear error pointing the user at `kubectl debug`.
- **Read-only rootfs**: zero impact — we don't write inside the pod for the bash path. `/tmp` is the only thing the prologue touches, and it's writable on the overwhelming majority of containers (emptyDir on read-only-rootfs setups).
- **Multi-container pod**: honor `kubectl.kubernetes.io/default-container` annotation; otherwise error with a list of container names and require `-c`.
- **Subprocess `kubectl` calls don't see shell aliases.** k9s, scripts, anything that shells out to `kubectl` from its own process won't have the wrapper applied. That's why k9s gets its own integration path; non-k9s subprocess callers fall back to vanilla `kubectl exec` behavior (no konch). Document; don't try to be clever.
- **Argv parsing tricky cases**: `kubectl exec -it pod -- bash -c 'something'` (the trailing `-c` makes it non-interactive — don't intercept). `kubectl exec -it pod -- /bin/bash` (absolute path; do intercept). `kubectl exec --tty=true --stdin=true pod -- bash` (long-form flags; intercept).
- **`Ctrl-P` collides with kubectl's default detach-keys**: document the `KUBECTL_DETACH_KEYS` workaround in README.
- **TERM passthrough**: ship local `$TERM` as `KONCH_LOCAL_TERM`; the prologue uses it when the pod has no TERM or `TERM=dumb`. v0.1 behavior, unchanged.
- **Pod not Running**: friendly error naming the phase (Pending / Failed / Succeeded), not a raw kubelet error.

## Build, test, ship

- `make build` — `go build -ldflags '-X main.version=...' -o konch ./cmd/konch`. Install step symlinks `kubectl-konch → konch`.
- `make test` — unit tests on shellparse (per shell), workload resolution, history merge, config defaults, init script generation.
- `make e2e` — integration against a `kind` cluster with one Deployment, one StatefulSet, one bare Pod, one CronJob-spawned pod.
- `make snapshot` — local goreleaser dry-run.
- Release: GoReleaser on tag; cross-compile linux/darwin × amd64/arm64; publish to GitHub release; goreleaser writes Homebrew formula to `christophersherman/homebrew-tap`; we manually PR `.krew.yaml` to `kubernetes-sigs/krew-index` per release.

## Style

- Idiomatic Go. `golangci-lint` clean (config at `.golangci.yaml`).
- Minimal deps: `k8s.io/*`, `github.com/spf13/cobra`, `github.com/BurntSushi/toml`. Resist adding more.
- Errors wrapped with `%w`. User-facing messages actionable first.
- One binary. No init scripts run automatically. No daemons.
- Comments are rare and explain *why*; well-named identifiers cover *what*.

## Out of scope (deferred to v0.3+)

- **OpenLens / Lens / VS Code k8s extension transparency.** Requires either an in-cluster mutating admission webhook (massive scope, per-user state living in the cluster) or per-tool cooperation we don't have. Document that those users fall back to explicit `konch <pod>`.
- **zsh / fish *inside* the pod.** Different from reading the user's local zsh/fish config — that's v0.2. In-pod zsh would require detecting + invoking zsh and a separate injection mechanism (zsh has no `--rcfile <(...)` equivalent).
- **`--copy-rcfile` for ash/dash environments** — let sh-only pods source a translated rcfile via `/tmp`, at the cost of zero-footprint.
- **`--debug` wrap of `kubectl debug`** for distroless containers.
- **`-E VAR1,VAR2` env passthrough** for arbitrary local env vars.
- **Streaming history mid-session** — so unexpected pod death doesn't lose the in-flight session. Today the session-end writeback exec is the only checkpoint.

## PR review checklist (apply to every change)

1. Does this add user-facing default behavior or opinionated config beyond "konch is the default for interactive pod exec"? → Reject.
2. Does this require writing inside the pod (outside `/tmp`)? → Reject, or hide behind an explicit flag with a loud warning.
3. Does another tool (k9s, kubectx, kubectl-iexec) already do this? → Defer.
4. Does this introduce a daemon, IPC, or persistent process? → Reject. State is files on disk.
5. Is this one more knob, or one more mechanism? → Mechanisms welcome; knobs scrutinized.
6. Does this change a user-facing string? → Preserve the voice. Actionable first, charming second.
7. Does this affect history mechanics, the shellrc parsers, or the in-pod prologue? → Add a unit test.

## README ordering

1. **Three-line hook**:
   - "Your aliases follow you into pods. Drop-in for `kubectl exec` after one-line setup."
   - "Bash history keyed by Deployment/StatefulSet, survives pod restarts and helm upgrades."
   - "Quality-of-life tool. No daemon, no cluster footprint, no opinion."
2. 30-second install:
   ```sh
   brew install christophersherman/tap/konch
   eval "$(konch init bash)"  # in your shellrc
   konch setup k9s
   ```
3. Demo gif.
4. How it works (mechanism — workload key, env-var-shipped rcfile, in-pod prologue, history pull).
5. `~/.config/konch/config.toml` reference table.
6. Per-tool integration matrix (kubectl ✓ transparent, k9s ✓ transparent, OpenLens ✗ fallback, scripts ✗ fallback).
7. Troubleshooting / FAQ.

Headline: **"Magic Konch — your shell, in the pod."** Tagline: *"All hail the Magic Konch."*

## See also (link from README)

- `xxh` — same idea for SSH; heavier (uploads a portable shell into the remote).
- `sshrc`, `kyrat` — same idea for SSH; rely on `/tmp` writes.
- `kubectl-iexec`, `kubectl-fuzzy` — pod pickers (complementary).
- `kube-ps1` — local prompt for kubectl context (complementary).
- `kuberc` (Kubernetes 1.33+) — client-side kubectl aliases. **Different layer**: konch ships your shell into the pod; kuberc aliases `kubectl` itself.
