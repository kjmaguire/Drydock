package wsl

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"
)

// RunEnv is the variable every process of one WSL agent run carries (value: the run's marker). Stopping the run kills every
// process of the distro that carries it, and the process groups they lead: killing wsl.exe on the Windows side (the Job
// Object) does not reliably end what it started inside the distro.
const RunEnv = "DRYDOCK_WSL_RUN"

// NewMarker returns a fresh run marker (random, not a secret: it only names the run's processes).
func NewMarker() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// launcher makes the program a session and process-group leader (setsid -w waits for it and returns its exit code, so wsl.exe
// lives exactly as long as the program) and falls back to a plain exec where setsid is missing.
const launcher = `if command -v setsid >/dev/null 2>&1; then exec setsid -w "$@"; fi; exec "$@"`

// Launch describes one program to start inside a distro.
type Launch struct {
	Distro string
	Dir    string // Linux working directory
	Marker string // the run marker (NewMarker); "" starts without one, and Kill cannot find the processes then
	// Env is set inside the distro on the command line (/usr/bin/env K=V ...). It is for values that are not secrets, such as
	// the Linux HOME and PATH: a command line is readable by every process of the machine. Secrets go through WSLENV
	// (Forward), which copies them from wsl.exe's environment.
	Env  []string
	Argv []string // the program (an absolute Linux path, or a name the PATH in Env finds) and its arguments
}

// Args is the argv after wsl.exe: wsl.exe -d <distro> --cd <dir> --exec /usr/bin/env <marker> <Env...> /bin/sh -c <launcher>
// drydock-run <Argv...>. --exec runs the program without a shell parsing the command line, so no argument is re-quoted.
func (l Launch) Args() []string {
	args := []string{"-d", l.Distro}
	if l.Dir != "" {
		args = append(args, "--cd", l.Dir)
	}
	args = append(args, "--exec", "/usr/bin/env")
	if l.Marker != "" {
		args = append(args, RunEnv+"="+l.Marker)
	}
	args = append(args, l.Env...)
	args = append(args, "/bin/sh", "-c", launcher, "drydock-run")
	return append(args, l.Argv...)
}

// killScript kills every process of the run (by the marker in its environment) and every process group one of them leads,
// a few rounds in case one was starting a child meanwhile, then prints how many it killed. With "list" it only prints the
// marked processes. Processes of other users and the script itself are never touched (their environment is unreadable, or
// carries no marker: the marker is in this script's arguments, not its environment).
const killScript = `m="$1"; mode="$2"; n=0
for pass in 1 2 3 4 5; do
  pids=""
  for d in /proc/[0-9]*; do
    p=${d#/proc/}
    [ "$p" = "$$" ] && continue
    if tr "\0" "\n" < "$d/environ" 2>/dev/null | grep -qx "DRYDOCK_WSL_RUN=$m"; then pids="$pids $p"; fi
  done
  if [ "$mode" = list ]; then echo "drydock-marked=$pids"; exit 0; fi
  [ -z "$pids" ] && break
  for p in $pids; do
    s=$(cat "/proc/$p/stat" 2>/dev/null) || continue
    s=${s##*) }; set -- $s
    case " $pids " in *" $3 "*) kill -KILL -- "-$3" 2>/dev/null;; esac
  done
  for p in $pids; do kill -KILL "$p" 2>/dev/null && n=$((n+1)); done
  sleep 0.2
done
echo "drydock-killed=$n"
`

// KillArgs is the argv after wsl.exe that kills every process of the run marked marker inside distro.
func KillArgs(distro, marker string) []string {
	return []string{"-d", distro, "--exec", "/bin/sh", "-c", killScript, "drydock-kill", marker, "kill"}
}

// ListArgs is the argv after wsl.exe that prints the processes of the run marked marker ("drydock-marked= <pid> ...").
func ListArgs(distro, marker string) []string {
	return []string{"-d", distro, "--exec", "/bin/sh", "-c", killScript, "drydock-kill", marker, "list"}
}

// Forward returns env (the environment wsl.exe starts with) with WSLENV set so that exactly the variables keep accepts are
// copied into the distro, the ones isPath accepts translated as paths (/p). Any WSLENV env had is replaced: what reaches the
// distro is Drydock's choice, not an inherited setting. Values stay in the environment, never on a command line.
func Forward(env []string, keep, isPath func(name string) bool) []string {
	out := make([]string, 0, len(env)+1)
	var names []string
	seen := map[string]bool{}
	for _, kv := range env {
		k, _, ok := strings.Cut(kv, "=")
		if !ok || strings.EqualFold(k, "WSLENV") {
			continue
		}
		out = append(out, kv)
		if k == "" || strings.ContainsAny(k, ":/") || seen[strings.ToUpper(k)] || !keep(k) {
			continue
		}
		seen[strings.ToUpper(k)] = true
		if isPath != nil && isPath(k) {
			names = append(names, k+"/p")
		} else {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	return append(out, "WSLENV="+strings.Join(names, ":"))
}
