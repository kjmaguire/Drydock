package sandbox

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/kjmaguire/drydock/internal/peer"
)

// Listening ports (effort review X31): the task page offers the ports something already listens on in the workspace, so the
// person forwards one with a click instead of finding it in the activity and typing it. The list is read from the workspace's
// own /proc (nothing is started, nothing leaves the machine); forwarding and opening stay the person's click (P134). What the
// workspace reports is the agent's doing, so only a port number and the name of a background process started with sandbox_start
// (a name the sandbox already checked) are kept.

// ListeningPort is a port something listens on in the workspace, on an address the forwarder can reach.
type ListeningPort struct {
	Port int `json:"port"`
	// Name is the background process (sandbox_start) that listens on it, when the workspace says so.
	Name string `json:"name,omitempty"`
}

// maxListening is the most listening ports listed.
const maxListening = 20

// listeningScript prints the workspace's TCP tables and, for each background process started with sandbox_start, the socket
// inodes held by it and the processes of its session (setsid makes it the session leader of what it starts).
const listeningScript = `echo '#tcp'; cat /proc/net/tcp 2>/dev/null; echo '#tcp6'; cat /proc/net/tcp6 2>/dev/null
for f in ` + bgDir + `/*.pid; do
  [ -f "$f" ] || continue
  n=${f##*/}; n=${n%.pid}; p=$(cat "$f" 2>/dev/null); [ -n "$p" ] || continue
  for d in /proc/[0-9]*; do
    read -r line 2>/dev/null < "$d/stat" || continue
    rest=${line##*) }; set -- $rest
    [ "$4" = "$p" ] || [ "${d#/proc/}" = "$p" ] || continue
    for fd in "$d"/fd/*; do
      l=$(readlink "$fd" 2>/dev/null) || continue
      case "$l" in socket:*) echo "#sock $n ${l#socket:}";; esac
    done
  done
done
exit 0`

// Listening lists the ports something listens on in the task's workspace, lowest first. A port bound to the workspace's own
// loopback address is left out: the forwarder cannot reach it.
func (m *Manager) Listening(ctx context.Context, taskID string) ([]ListeningPort, error) {
	b, err := m.Existing(ctx, taskID)
	if err != nil {
		return nil, err
	}
	out, err := m.Engine.Run(ctx, nil, "exec", b.Workspace, "sh", "-c", listeningScript)
	if err != nil {
		return nil, err
	}
	return ParseListening(out), nil
}

// ParseListening reads listeningScript's output.
func ParseListening(out string) []ListeningPort {
	var tcp, tcp6 strings.Builder
	cur := (*strings.Builder)(nil)
	names := map[uint64]string{}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case line == "#tcp":
			cur = &tcp
			continue
		case line == "#tcp6":
			cur = &tcp6
			continue
		case strings.HasPrefix(line, "#sock "):
			cur = nil
			f := strings.Fields(line)
			if len(f) != 3 || !bgNameRe.MatchString(f[1]) {
				continue
			}
			inode, err := strconv.ParseUint(strings.Trim(f[2], "[]"), 10, 64)
			if err != nil {
				continue
			}
			if _, ok := names[inode]; !ok {
				names[inode] = f[1]
			}
			continue
		}
		if cur != nil {
			cur.WriteString(line)
			cur.WriteByte('\n')
		}
	}
	byPort := map[int]ListeningPort{}
	for i, text := range []string{tcp.String(), tcp6.String()} {
		rows, err := peer.ParseProcNetTCP(text, i == 1)
		if err != nil {
			continue
		}
		for _, r := range rows {
			addr := r.Local.Addr().Unmap()
			if !r.Listen || addr.IsLoopback() || r.Local.Port() == 0 {
				continue
			}
			p := int(r.Local.Port())
			lp := byPort[p]
			lp.Port = p
			if lp.Name == "" {
				lp.Name = names[r.Inode]
			}
			byPort[p] = lp
		}
	}
	list := make([]ListeningPort, 0, len(byPort))
	for _, lp := range byPort {
		list = append(list, lp)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Port < list[j].Port })
	if len(list) > maxListening {
		list = list[:maxListening]
	}
	return list
}
