package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/safe"
)

// Relay forwards TCP connections from a listener to one fixed address. It is the forwarder container of a sandbox's published
// port (decision P134): the container sits on the task's internal network and on a bridge network that publishes 127.0.0.1 only,
// and the relay is all it runs. The target is fixed when it starts, so a connection can reach nothing else: not the proxy, not a
// service, not another host.
type Relay struct {
	// Target is host:port, dialled for every connection.
	Target string
	// MaxConns bounds the connections open at once (default 64); one more is closed at once.
	MaxConns int
	// IdleTimeout closes a connection that carried nothing for this long (default 10 minutes).
	IdleTimeout time.Duration
	// DialTimeout bounds reaching the target (default 5 seconds).
	DialTimeout time.Duration
}

// Serve accepts connections on l until ctx ends or l fails, then waits for the open connections to finish.
func (r *Relay) Serve(ctx context.Context, l net.Listener) error {
	max := r.MaxConns
	if max <= 0 {
		max = 64
	}
	slots := make(chan struct{}, max)
	var wg sync.WaitGroup
	safe.Go("egress.Relay.Serve", func() {
		<-ctx.Done()
		_ = l.Close()
	})
	defer wg.Wait()
	for {
		c, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		select {
		case slots <- struct{}{}:
		default:
			_ = c.Close()
			continue
		}
		wg.Add(1)
		safe.Go("egress.Relay.handle", func() {
			defer wg.Done()
			defer func() { <-slots }()
			r.handle(ctx, c)
		})
	}
}

func (r *Relay) handle(ctx context.Context, in net.Conn) {
	defer func() { _ = in.Close() }()
	dt := r.DialTimeout
	if dt <= 0 {
		dt = 5 * time.Second
	}
	d := net.Dialer{Timeout: dt}
	out, err := d.DialContext(ctx, "tcp", r.Target)
	if err != nil {
		return // the app is not listening (yet): the person's browser sees a refused connection
	}
	defer func() { _ = out.Close() }()
	idle := r.IdleTimeout
	if idle <= 0 {
		idle = 10 * time.Minute
	}
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		buf := make([]byte, 32<<10)
		for {
			_ = src.SetReadDeadline(time.Now().Add(idle))
			n, err := src.Read(buf)
			if n > 0 {
				_ = dst.SetWriteDeadline(time.Now().Add(idle))
				if _, werr := dst.Write(buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		// this direction is over: the far side is told, and the other direction may still be answering (a client that closed its
		// writing end waits for the reply)
		if t, ok := dst.(*net.TCPConn); ok {
			_ = t.CloseWrite()
		} else {
			_ = dst.Close()
		}
		done <- struct{}{}
	}
	safe.Go("egress.Relay.copy", func() { cp(out, in) })
	safe.Go("egress.Relay.copy#2", func() { cp(in, out) })
	stopped := make(chan struct{})
	safe.Go("egress.Relay.stop", func() {
		select {
		case <-ctx.Done():
			_ = in.Close()
			_ = out.Close()
		case <-stopped:
		}
	})
	<-done
	<-done
	close(stopped)
}

// RelayFromEnv builds the relay and its listen address from DRYDOCK_RELAY_LISTEN (":8080") and DRYDOCK_RELAY_TARGET
// ("drydock-task-ws:3000").
func RelayFromEnv(getenv func(string) string) (*Relay, string, error) {
	listen, target := getenv("DRYDOCK_RELAY_LISTEN"), getenv("DRYDOCK_RELAY_TARGET")
	if _, port, err := net.SplitHostPort(listen); err != nil || !validPort(port) {
		return nil, "", fmt.Errorf("DRYDOCK_RELAY_LISTEN %q is not an address with a port", listen)
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil || host == "" || !validPort(port) {
		return nil, "", fmt.Errorf("DRYDOCK_RELAY_TARGET %q is not host:port", target)
	}
	return &Relay{Target: target}, listen, nil
}

func validPort(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1 && n <= 65535
}
