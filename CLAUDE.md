# CLAUDE.md — Magic Konch (`kubectl-konch`)

## What you're building

A kubectl plugin that brings the user's local shell config into pods when they `exec` in. The project is **Magic Konch**, the CLI is **`konch`**.

**Invocation:**
```
kubectl konch <pod> [-c container]
```
Execs into the pod and starts an interactive bash session pre-loaded with the user's rcfile. Bash history is keyed by the pod's controlling workload and persisted **locally on the host**, so it survives pod restarts.

## Name & the bit

The project name is a SpongeBob reference (the Magic Conch episode), with the `K` doing double duty for Kubernetes and for the conch *shell* — which is what we're bringing into the pod. The bit is part of the product, not decoration. It is intentionally on-brand for the ecosystem (stern, popeye, k9s, ksniff) and should land everywhere a string is user-visible: README headline, `--help` text, `--probe` output, error messages.

**Voice and tone:**
- Light touch, never overdone. The bit lands once or twice per surface, not in every sentence.
- The Konch is *passive and a little unhelpful*, like in the show. That voice fits a "mechanism not opinion" tool perfectly — the Konch doesn't tell you what to do, it just relays what's already true.
- Examples that work: `--probe` ending with `~ the Konch has spoken ~`, an empty-rcfile warning that says `Try asking again.`, a workload-not-found case with `Nothing.`. One per surface, no more.
- What doesn't work: stuffing references everywhere, in-jokes that require show knowledge to read the output, anything that obscures actionable info. The bit is seasoning; the message is the meal.
- All error messages must still be actionable first, charming second. "bash not found in pod, falling back to sh" is the message; the personality goes in the help text and probe output, not in error paths the user needs to debug.

## Philosophy: mechanism, not opinion

Non-negotiable. Konch injects whatever the user puts in their rcfile. It does not ship default aliases. It does not pick favorite tools. It exposes mechanism and config hooks; the user decides everything else.

**Reject these features no matter how tempting they look:**
- Default aliases (`grep`→`rg`, `ll`, etc.) — the user owns their rcfile.
- fzf / interactive pod picker — k9s and `kubectl-iexec` already do this.
- Default opinionated prompt — expose env vars instead and let the rcfile build a prompt.
- Bundled tools (ripgrep, bat, etc.) — violates zero-footprint.
- AI features, "explain this pod", LLM integrations — feature creep.

## v0.1 feature set (everything below ships, nothing else)

1. **Rcfile injection via process substitution.** Inject the user's rcfile via `bash --rcfile <(...)`. Nothing is written inside the pod. Zero footprint, works on read-only rootfs.

2. **Workload-keyed bash history.** Walk `ownerReferences` to find the controlling workload (Deployment, StatefulSet, DaemonSet, CronJob, Job, ReplicaSet). Fall back to the `app.kubernetes.io/name` label. Fall back to pod name. Store history on the host at `~/.local/share/konch/history/<context>/<namespace>/<workload>`.

3. **Shell auto-detect.** Probe for bash; fall back to sh. On sh, skip alias-only lines in the rcfile with one warning to stderr.

4. **`--probe` dry-run.** Don't exec. Report: detected shell, container chosen, workload key, which rcfile would be sourced, which lines would be skipped, final command that would be invoked. (End the output with one Konch-voice line.)

5. **Per-context layered config.** Merge in order: `~/.config/konch/rc` (always), then `~/.config/konch/rc.<context>` if present. Last write wins.

6. **Prompt-context env vars.** Set these before invoking bash so the rcfile can build a prompt:
   `KONCH_POD`, `KONCH_NAMESPACE`, `KONCH_CONTEXT`, `KONCH_WORKLOAD`, `KONCH_CONTAINER`.

7. **Container selection.** `-c/--container` flag. Default: respect the `kubectl.kubernetes.io/default-container` annotation if present, else the first container.

## Architecture

**Language:** Go. Single static binary. No runtime dependencies.

**Pattern:** Follow `kubernetes/sample-cli-plugin`. Use `k8s.io/cli-runtime` for kubeconfig loading and namespace resolution. Use `k8s.io/client-go` for the pod fetch and ownerReferences walk. Use `k8s.io/client-go/tools/remotecommand` for the exec stream (SPDY; let it negotiate WebSocket where available). Wire up stdin/stdout/stderr and TTY resize signals correctly.

**Project layout:**
```
cmd/kubectl-konch/main.go     # entrypoint, flag parsing
internal/exec/                # remotecommand wiring, TTY handling
internal/workload/            # ownerReferences walk + fallback chain
internal/rcfile/              # local rcfile loading, layered merge
internal/probe/               # --probe mode
internal/shell/               # bash-vs-sh detection
go.mod
.krew.yaml
.goreleaser.yaml
README.md
LICENSE                       # Apache 2.0
```

## Key technical details

### The injection trick

The user's rcfile content is base64-encoded on the host and sent through the exec command line. Inside the pod, decode and feed it into bash via process substitution:

```sh
exec bash --rcfile <(echo "$KONCH_RC_B64" | base64 -d) -i
```

`KONCH_RC_B64` is set in the env passed through the exec stream. Base64 avoids quoting hell with arbitrary rcfile contents.

If the user's rcfile is empty or missing, skip `--rcfile` entirely and just `exec bash -i`. (Konch voice: `Nothing to bring. Starting a plain shell.`)

### Workload key resolution

```
pod.metadata.ownerReferences[0].kind:
  ReplicaSet  → fetch RS; if RS has owner Deployment, return Deployment name; else RS name
  StatefulSet → return name
  DaemonSet   → return name
  Job         → fetch Job; if Job has owner CronJob, return CronJob name; else Job name
  (none/other) → fall back to label app.kubernetes.io/name; else pod name
```

Cache the workload lookup per pod for the session. Total cost should be at most one extra GET (for the ReplicaSet→Deployment or Job→CronJob hop).

### Edge cases

- **Alpine / dash / ash:** no process substitution. Detect bash absence; warn once to stderr; skip rcfile injection and exec a plain shell (user is no worse off than `kubectl exec`).
- **Distroless:** no shell at all. If `command -v sh` fails inside the pod, print a clear error pointing the user at `kubectl debug`. Don't try to be clever.
- **Read-only rootfs:** zero impact — we never write inside the pod.
- **`Ctrl-P` collides with kubectl detach-keys:** out of scope. Document the workaround in README.
- **TERM passthrough:** set `TERM` from local env if not already present in the exec environment.
- **Multi-container pods:** honor `kubectl.kubernetes.io/default-container` annotation; warn if `-c` is missing and there are multiple containers without the annotation.

## Build, test, ship

- `make build` — `go build ./cmd/kubectl-konch`
- `make test` — unit tests for workload resolution, rcfile merging, shell detection.
- `make e2e` — integration test against a `kind` cluster with one Deployment, one StatefulSet, one bare Pod.
- Release: GoReleaser on git tag; cross-compile for linux/darwin × amd64/arm64.
- Distribution: Homebrew tap first; submit `.krew.yaml` to `kubernetes-sigs/krew-index` after the first stable release.

## Style

- Idiomatic Go. `golangci-lint` clean.
- Minimal deps: `k8s.io/*` and `github.com/spf13/cobra`. Stdlib flag is fine too if cobra feels heavy.
- Errors wrapped with `%w`. User-facing messages must be actionable first.
- One binary. No init scripts. No helper services. No daemons.

## Out of scope for v0.1 (defer to v0.2+)

- `--copy-rcfile` fallback that writes to `/tmp` (for ash/dash environments).
- `--debug` flag that wraps `kubectl debug` for distroless containers.
- `-E VAR1,VAR2` to pass through named local env vars.
- Shell completion injection.
- zsh/fish support inside the pod.

## PR review checklist (apply to every change)

1. Does this add user-facing default behavior or opinionated config? → Reject.
2. Does this require writing inside the pod? → Reject, or hide behind an explicit flag with a loud warning.
3. Does another tool (k9s, kubectx, kubectl-iexec) already do this? → Defer.
4. Is this one more knob, or one more mechanism? → Mechanisms welcome; knobs scrutinized.
5. Does this change a user-facing string? → Preserve the voice. Actionable first, charming second. Don't add Konch references in a place that didn't already have one.

## README ordering (when you write it)

Hook readers in this exact order — the first three lines do the work:

1. Your aliases follow you into pods. Zero footprint.
2. Bash history that survives pod restarts, keyed by Deployment/StatefulSet, not pod hash.
3. Prompt context for free: `$KONCH_POD`, `$KONCH_NAMESPACE`, `$KONCH_WORKLOAD` in your rcfile.
4. Plays nice with read-only rootfs and distroless.
5. `--probe` mode for safety.
6. Per-context layered config.
7. One-line install (Krew + Homebrew) and a 30-second demo gif.

Headline: **"Magic Konch — your shell, in the pod."** Tagline candidate: *"All hail the Magic Konch."* Put a small ASCII Konch in the header if you're feeling it.

## See also (link from README)

- `xxh` — same idea for SSH; heavier (uploads a portable shell).
- `sshrc` / `kyrat` — same idea for SSH; rely on `/tmp` writes.
- `kubectl-iexec`, `kubectl-fuzzy` — pod pickers (complementary).
- `kube-ps1` — local prompt for kubectl context (complementary).
- `kuberc` (Kubernetes 1.33+) — client-side kubectl aliases; **different layer** from Konch. Mention explicitly to head off confusion.