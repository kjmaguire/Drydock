package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Port forwarding (decision P134): the person asks for a port of a task's sandbox on the task page and a forwarder container
// relays it to 127.0.0.1 on this machine. The workspace and the services sit on an internal network, whose containers Docker does
// not publish, so the forwarder is attached to that network and to a bridge network of its own that publishes the port on the
// loopback address only. It relays to the workspace's port and to nothing else (egress.Relay). It carries the task's labels, so it
// goes with the sandbox, and it is the record: what is forwarded is what the engine lists.

// LabelForward is the label of a forwarder container; its value is the workspace port it relays to.
const LabelForward = "drydock.forward"

// MaxForwards is the most ports one task forwards at once.
const MaxForwards = 5

// relayListen is the port the relay listens on inside its container: above 1023, so it needs no privilege.
const relayListen = 8080

// Forward is a forwarded port.
type Forward struct {
	TaskID string `json:"taskId"`
	// Port is the port of the app in the workspace.
	Port int `json:"port"`
	// HostPort is the port on this machine's loopback address that reaches it.
	HostPort int `json:"hostPort"`
	// Address is where the person's own window opens it: 127.0.0.1:<HostPort>.
	Address   string `json:"address"`
	Container string `json:"container"`
}

// ErrNoSandbox means the task has no running sandbox to forward from.
var ErrNoSandbox = errors.New("this task has no running sandbox")

// ValidForwardPort reports whether n can be a port of the workspace's app.
func ValidForwardPort(n int) error {
	if n < 1 || n > 65535 {
		return fmt.Errorf("%d is not a port (1 to 65535)", n)
	}
	return nil
}

func forwardName(b *Box, port int) string { return fmt.Sprintf("%s-fwd-%d", b.Base, port) }

// Existing returns the task's running sandbox as the engine has it, whoever created it (this start of Drydock or an earlier one).
func (m *Manager) Existing(ctx context.Context, taskID string) (*Box, error) {
	base := BaseName(taskID)
	ws := base + "-ws"
	out, err := m.Engine.Run(ctx, nil, "inspect", "--format", "{{.State.Running}}|{{index .Config.Labels \""+LabelTask+"\"}}|{{index .Config.Labels \""+LabelProject+"\"}}|{{index .Config.Labels \""+LabelOwner+"\"}}", ws)
	if err != nil {
		if errors.Is(err, ErrNoEngine) {
			return nil, err
		}
		return nil, ErrNoSandbox
	}
	f := strings.SplitN(strings.TrimSpace(out), "|", 4)
	// the name is derived from the task id, so two ids can in principle share a prefix: the label says whose it is
	if len(f) != 4 || f[0] != "true" || f[1] != taskID || f[3] != "drydock" {
		return nil, ErrNoSandbox
	}
	return &Box{Spec: Spec{TaskID: taskID, ProjectID: f[2]}, Base: base, Workspace: ws, Network: base}, nil
}

// Forwards lists the task's forwarded ports, lowest first.
func (m *Manager) Forwards(ctx context.Context, taskID string) ([]Forward, error) {
	labels, err := m.Engine.labelsOf(ctx, LabelTask+"="+taskID)
	if err != nil {
		return nil, err
	}
	var out []Forward
	for id, l := range labels {
		if l[LabelOwner] != "drydock" || l[LabelRole] != "forward" || l[LabelTask] != taskID {
			continue
		}
		port, err := strconv.Atoi(l[LabelForward])
		if err != nil {
			continue
		}
		host, ok := m.publishedPort(ctx, id)
		if !ok {
			continue // a forwarder that is not running publishes nothing
		}
		out = append(out, Forward{TaskID: taskID, Port: port, HostPort: host, Address: net.JoinHostPort("127.0.0.1", strconv.Itoa(host)), Container: BaseName(taskID) + "-fwd-" + l[LabelForward]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out, nil
}

// publishedPort is the loopback port a forwarder container is published on. A container that publishes on any other address (a
// bug here, or a changed engine default) reports none: the page would be open to the network.
func (m *Manager) publishedPort(ctx context.Context, container string) (int, bool) {
	out, err := m.Engine.Run(ctx, nil, "port", container, strconv.Itoa(relayListen)+"/tcp")
	if err != nil {
		return 0, false
	}
	port := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		ap, err := netip.ParseAddrPort(strings.TrimSpace(line))
		if err != nil {
			return 0, false
		}
		if !ap.Addr().IsLoopback() {
			return 0, false
		}
		port = int(ap.Port())
	}
	return port, port != 0
}

// OpenForward forwards port of the task's sandbox. Asking again for a port already forwarded returns the same forward.
func (m *Manager) OpenForward(ctx context.Context, taskID string, port int) (Forward, error) {
	if err := ValidForwardPort(port); err != nil {
		return Forward{}, err
	}
	l := m.lock(taskID)
	l.Lock()
	defer l.Unlock()
	b, err := m.Existing(ctx, taskID)
	if err != nil {
		return Forward{}, err
	}
	have, err := m.Forwards(ctx, taskID)
	if err != nil {
		return Forward{}, err
	}
	for _, f := range have {
		if f.Port == port {
			return f, nil
		}
	}
	if len(have) >= MaxForwards {
		return Forward{}, fmt.Errorf("a task forwards at most %d ports: stop one first", MaxForwards)
	}
	info, err := m.Info(ctx)
	if err != nil {
		return Forward{}, err
	}
	image, err := m.forwarderImage(ctx, info.Arch)
	if err != nil {
		return Forward{}, err
	}
	fwdNet := b.Base + "-fwd"
	// the bridge network the port is published through; a task has one for all its forwarders
	if _, err := m.Engine.Run(ctx, nil, "network", "inspect", "--format", "{{.Id}}", fwdNet); err != nil {
		if _, err := m.Engine.Run(ctx, nil, append(append([]string{"network", "create"}, m.labels(b, "network", "")...), fwdNet)...); err != nil {
			return Forward{}, err
		}
	}
	name := forwardName(b, port)
	_ = m.removeContainer(ctx, name) // one left stopped by an earlier start
	args := []string{"run", "-d", "--name", name, "--network", fwdNet, "-p", "127.0.0.1::" + strconv.Itoa(relayListen)}
	args = append(args, m.labels(b, "forward", "")...)
	args = append(args, "--label", LabelForward+"="+strconv.Itoa(port),
		"--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--memory", "64m", "--pids-limit", "128", "--user", "65534:65534",
		"-e", "DRYDOCK_RELAY_LISTEN=:"+strconv.Itoa(relayListen), "-e", "DRYDOCK_RELAY_TARGET="+net.JoinHostPort(b.Workspace, strconv.Itoa(port)), image, "relay")
	if _, err := m.Engine.Run(ctx, nil, args...); err != nil {
		return Forward{}, err
	}
	fail := func(err error) (Forward, error) {
		_ = m.removeContainer(context.WithoutCancel(ctx), name)
		return Forward{}, err
	}
	// the workspace is reached by its name on the task's internal network, which the forwarder joins after it was published
	if _, err := m.Engine.Run(ctx, nil, "network", "connect", b.Network, name); err != nil {
		return fail(err)
	}
	var host int
	var ok bool
	for i := 0; i < 20 && !ok; i++ { // the engine publishes a moment after the container starts
		if host, ok = m.publishedPort(ctx, name); !ok {
			select {
			case <-ctx.Done():
				return fail(ctx.Err())
			case <-time.After(150 * time.Millisecond):
			}
		}
	}
	if !ok {
		return fail(errors.New("the engine did not publish the port on the loopback address (127.0.0.1)"))
	}
	return Forward{TaskID: taskID, Port: port, HostPort: host, Address: net.JoinHostPort("127.0.0.1", strconv.Itoa(host)), Container: name}, nil
}

// CloseForward stops forwarding port. A port that is not forwarded is not an error.
func (m *Manager) CloseForward(ctx context.Context, taskID string, port int) error {
	l := m.lock(taskID)
	l.Lock()
	defer l.Unlock()
	return m.removeContainer(ctx, BaseName(taskID)+"-fwd-"+strconv.Itoa(port))
}

func (m *Manager) removeContainer(ctx context.Context, name string) error {
	if _, err := m.Engine.Run(ctx, nil, "inspect", "--format", "{{.Id}}", name); err != nil {
		return nil
	}
	_, err := m.Engine.Run(ctx, nil, "rm", "-f", "-v", name)
	return err
}

// forwarderImage is the image of the forwarder: the egress program, run in its relay mode.
func (m *Manager) forwarderImage(ctx context.Context, arch string) (string, error) {
	find := m.EgressBin
	if find == nil {
		find = EgressBinary
	}
	p, err := find(arch)
	if err != nil {
		return "", fmt.Errorf("port forwarding needs the drydock-egress-linux-%s program that ships next to Drydock (or DRYDOCK_EGRESS_BIN): %v", arch, err)
	}
	return m.importEgress(ctx, p)
}
