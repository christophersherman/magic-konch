// Package cliinit emits the shell snippets that `konch init <shell>`
// prints to stdout. Users wire them into their shellrc with `eval`:
//
//	eval "$(konch init bash)"   # in ~/.bashrc
//	eval "$(konch init zsh)"    # in ~/.zshrc
//
// After that, `kubectl exec -it <pod> -- bash` (and equivalent shapes)
// transparently routes through `konch <pod>`. Other kubectl invocations
// pass through unmodified.
//
// The bash and zsh snippets share a POSIX-clean implementation — the
// argv-parsing helper uses only constructs both shells (and dash) accept.
// fish needs a separate native version because its syntax differs.
package cliinit

// Bash returns the bash wrapper snippet.
func Bash() string { return posixSnippet("bash") }

// Zsh returns the zsh wrapper snippet. Same body as Bash — both shells
// share enough surface that one POSIX-clean implementation covers both.
func Zsh() string { return posixSnippet("zsh") }

// posixSnippet returns the kubectl()-wrapping shell function suitable
// for sourcing under bash or zsh via `eval "$(konch init ...)"`.
//
// The wrapper inspects argv: if it matches kubectl [<flags>] exec
// [<flags>] <pod> [-c <c>] [-n <ns>] -- (bash|sh|zsh|fish)?, it routes
// to `command konch <pod>` with the right --container/--namespace/
// --context. Anything else falls through to `command kubectl "$@"`.
//
// Argv parsing happens in a helper function (__konch_detect) so the
// outer kubectl() function keeps its $@ intact for the passthrough case.
func posixSnippet(shell string) string {
	return "# konch: transparent kubectl-exec wrapper for " + shell + ".\n" +
		"# Source via: eval \"$(konch init " + shell + ")\"\n" + posixBody
}

const posixBody = `
__konch_detect() {
  __konch_intercept=0
  __konch_saw_exec=0
  __konch_pod=""
  __konch_cont=""
  __konch_ns=""
  __konch_ctx=""
  __konch_lead=""
  __konch_tcount=0
  __konch_post_dd=0

  while [ $# -gt 0 ]; do
    if [ $__konch_post_dd -eq 1 ]; then
      __konch_tcount=$((__konch_tcount + 1))
      [ $__konch_tcount -eq 1 ] && __konch_lead="$1"
      shift
      continue
    fi
    case "$1" in
      --) __konch_post_dd=1 ;;
      exec) __konch_saw_exec=1 ;;
      -c|--container) shift; __konch_cont="$1" ;;
      --container=*) __konch_cont="${1#--container=}" ;;
      -n|--namespace) shift; __konch_ns="$1" ;;
      --namespace=*) __konch_ns="${1#--namespace=}" ;;
      --context) shift; __konch_ctx="$1" ;;
      --context=*) __konch_ctx="${1#--context=}" ;;
      -*) ;;
      *) [ $__konch_saw_exec -eq 1 ] && [ -z "$__konch_pod" ] && __konch_pod="$1" ;;
    esac
    shift
  done

  if [ $__konch_saw_exec -eq 1 ] && [ -n "$__konch_pod" ] && [ $__konch_tcount -le 1 ]; then
    case "$__konch_lead" in
      "" | bash | sh | zsh | fish | /bin/bash | /bin/sh | /bin/zsh | /usr/bin/fish | /usr/local/bin/fish)
        __konch_intercept=1
        ;;
    esac
  fi
}

kubectl() {
  __konch_detect "$@"
  if [ "$__konch_intercept" = "1" ]; then
    set --
    [ -n "$__konch_cont" ] && set -- "$@" --container "$__konch_cont"
    [ -n "$__konch_ns" ] && set -- "$@" --namespace "$__konch_ns"
    [ -n "$__konch_ctx" ] && set -- "$@" --context "$__konch_ctx"
    command konch "$__konch_pod" "$@"
    return $?
  fi
  command kubectl "$@"
}
`
