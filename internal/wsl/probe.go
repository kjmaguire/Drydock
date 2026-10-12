package wsl

import (
	"strconv"
	"strings"
	"time"
)

// probeMark starts every line the probe script reports; anything else (a login banner, a profile's echo) is ignored.
const probeMark = "@@drydock@@ "

// probeScript runs under /bin/sh inside the distro. It finds the person's login shell and runs the report through it as a
// login shell, because a CLI installed for one user (claude in ~/.local/bin) is on the PATH only after ~/.profile ran. A shell
// that does not speak POSIX sh (fish, nu, csh) is replaced by bash, else sh, also as a login shell. Arguments: the program
// to look for, its version arguments and its sign-in status arguments (each a space-separated word list from a manifest,
// split with eval because zsh does not split an unquoted variable). It never reads a
// credential file: the sign-in state is what the CLI's own status command prints.
const probeScript = `t="$1"; va="$2"; aa="$3"
u=$(id -un 2>/dev/null)
s=$(getent passwd "$u" 2>/dev/null | cut -d: -f7)
case "${s##*/}" in fish|nu|csh|tcsh|xonsh|elvish|pwsh|"") s="";; esac
[ -n "$s" ] && [ -x "$s" ] || s=$(command -v bash 2>/dev/null)
[ -n "$s" ] || s=/bin/sh
exec "$s" -lc '
say() { printf "\n@@drydock@@ %s=%s\n" "$1" "$2"; }
t="$1"; va="$2"; aa="$3"
say shell "$4"
say home "$HOME"
say path "$PATH"
say user "$(id -un 2>/dev/null)"
say uid "$(id -u 2>/dev/null)"
say kernel "$(uname -r 2>/dev/null)"
say distro "$WSL_DISTRO_NAME"
say mountc "$(wslpath -u "C:\\" 2>/dev/null)"
if [ -r /etc/wsl.conf ]; then while IFS= read -r l || [ -n "$l" ]; do say wslconf "$l"; done < /etc/wsl.conf; fi
if [ -e /proc/sys/fs/binfmt_misc/WSLInterop ] || [ -e /proc/sys/fs/binfmt_misc/WSLInterop-late ]; then say interop 1; else say interop 0; fi
say netmode "$(wslinfo --networking-mode 2>/dev/null)"
say setsid "$(command -v setsid 2>/dev/null)"
say git "$(git --version 2>/dev/null)"
c=$(command -v "$t" 2>/dev/null)
case "$c" in /*) ;; *) c="";; esac
for d in "$HOME/.local/bin" "$HOME/.claude/local" "$HOME/.npm-global/bin" /usr/local/bin; do
  if [ -z "$c" ] && [ -x "$d/$t" ]; then c="$d/$t"; fi
done
say tool "$c"
if [ -n "$c" ]; then
  v=$(eval "\"\$c\" $va" 2>&1 </dev/null | head -n 5 | tr "\n" " ")
  say version "$v"
  if [ -n "$aa" ]; then
    a=$(eval "\"\$c\" $aa" 2>/dev/null </dev/null); e=$?
    say authexit "$e"
    say auth "$(printf "%s" "$a" | tr "\n" " ")"
  fi
fi
say done 1
' drydock-probe "$t" "$va" "$aa" "$s"
`

// ProbeArgs is the argv after wsl.exe that runs the probe for tool in distro.
func ProbeArgs(distro, tool string, versionArgs, authArgs []string) []string {
	return []string{"-d", distro, "--exec", "/bin/sh", "-c", probeScript, "drydock-probe", tool, strings.Join(versionArgs, " "), strings.Join(authArgs, " ")}
}

// Info is what the probe found in one distro. It holds no credential: Auth is the CLI's own status output (signed in or not,
// how), which never contains a token.
type Info struct {
	Distro  string `json:"distro"`
	Shell   string `json:"shell,omitempty"`   // the login shell the probe ran
	Home    string `json:"home,omitempty"`    // the Linux HOME
	Path    string `json:"path,omitempty"`    // the Linux PATH of a login shell
	User    string `json:"user,omitempty"`    // the distro's default user
	Kernel  string `json:"kernel,omitempty"`  // uname -r
	WSL2    bool   `json:"wsl2"`              // a WSL 2 kernel (WSL 1 has no Linux kernel of its own)
	Interop bool   `json:"interop"`           // Windows programs can be started from inside (Drydock's MCP shim needs it)
	NetMode string `json:"netMode,omitempty"` // nat, mirrored, ... from wslinfo ("" unknown)
	Setsid  bool   `json:"setsid"`            // setsid exists, so an agent gets a process group of its own
	Git     string `json:"git,omitempty"`     // git --version inside the distro
	Paths   Paths  `json:"paths"`
	// Tool is the absolute path of the CLI inside the distro ("" not found); Version its version output; Auth and AuthExit
	// what its sign-in status command printed and how it exited (AuthExit -1 when it was not run).
	Tool     string    `json:"tool,omitempty"`
	Version  string    `json:"version,omitempty"`
	Auth     string    `json:"auth,omitempty"`
	AuthExit int       `json:"authExit"`
	Complete bool      `json:"complete"` // the script ran to its end
	At       time.Time `json:"at"`
}

// ParseProbe reads the probe script's report.
func ParseProbe(distro, out string) Info {
	in := Info{Distro: distro, AuthExit: -1, Paths: Paths{Distro: distro}}
	var mountc string
	var conf []string
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		if !strings.HasPrefix(l, probeMark) {
			continue
		}
		k, v, _ := strings.Cut(l[len(probeMark):], "=")
		v = strings.TrimSpace(v)
		switch k {
		case "shell":
			in.Shell = v
		case "home":
			in.Home = v
		case "path":
			in.Path = v
		case "user":
			in.User = v
		case "kernel":
			in.Kernel = v
			in.WSL2 = strings.Contains(strings.ToLower(v), "wsl2") || strings.Contains(v, "microsoft-standard")
		case "mountc":
			mountc = v
		case "wslconf":
			conf = append(conf, v)
		case "interop":
			in.Interop = v == "1"
		case "netmode":
			in.NetMode = strings.ToLower(v)
		case "setsid":
			in.Setsid = v != ""
		case "git":
			in.Git = v
		case "tool":
			in.Tool = v
		case "version":
			in.Version = v
		case "authexit":
			if n, err := strconv.Atoi(v); err == nil {
				in.AuthExit = n
			}
		case "auth":
			in.Auth = v
		case "done":
			in.Complete = true
		}
	}
	in.Paths.MountRoot = MountRootFrom(mountc, conf)
	if v, ok := ConfValue(conf, "interop", "enabled"); ok && strings.EqualFold(v, "false") {
		in.Interop = false
	}
	return in
}
