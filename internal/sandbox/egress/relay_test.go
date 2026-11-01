package egress

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// echoServer answers every connection with what it reads, upper-cased.
func echoServer(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				b, _ := io.ReadAll(c)
				_, _ = c.Write([]byte(strings.ToUpper(string(b))))
			}()
		}
	}()
	return l
}

func startRelay(t *testing.T, r *Relay) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Serve(ctx, l) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the relay did not stop")
		}
	})
	return l.Addr().String()
}

func TestRelayCarriesBytesBothWaysToItsOneTarget(t *testing.T) {
	target := echoServer(t)
	addr := startRelay(t, &Relay{Target: target.Addr().String()})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("hello through the relay")); err != nil {
		t.Fatal(err)
	}
	c.(*net.TCPConn).CloseWrite()
	got, err := io.ReadAll(c)
	if err != nil || string(got) != "HELLO THROUGH THE RELAY" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestRelayClosesAConnectionWhenTheTargetIsNotListening(t *testing.T) {
	dead, _ := net.Listen("tcp", "127.0.0.1:0")
	target := dead.Addr().String()
	_ = dead.Close()
	addr := startRelay(t, &Relay{Target: target, DialTimeout: time.Second})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := c.Read(make([]byte, 1)); err == nil || n != 0 {
		t.Fatalf("a connection to a dead target is closed: %d %v", n, err)
	}
}

func TestRelayRefusesConnectionsOverItsLimit(t *testing.T) {
	// a target that holds its connections open
	hold, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hold.Close() })
	go func() {
		for {
			c, err := hold.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(io.Discard, c) }()
		}
	}()
	addr := startRelay(t, &Relay{Target: hold.Addr().String(), MaxConns: 2})
	var open []net.Conn
	t.Cleanup(func() {
		for _, c := range open {
			_ = c.Close()
		}
	})
	for i := 0; i < 2; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		open = append(open, c)
	}
	time.Sleep(200 * time.Millisecond) // both are accepted and dialled through
	third, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	_ = third.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := third.Read(make([]byte, 1)); err == nil {
		t.Fatal("the third connection is over the limit and is closed")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("the third connection was left open")
	}
}

func TestRelayClosesAnIdleConnection(t *testing.T) {
	hold, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { _ = hold.Close() })
	go func() {
		for {
			c, err := hold.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(io.Discard, c) }()
		}
	}()
	addr := startRelay(t, &Relay{Target: hold.Addr().String(), IdleTimeout: 300 * time.Millisecond})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("expected the idle connection to be closed")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("the idle connection was not closed")
	}
}

// A connection that only one side talks on is not idle: an event stream or a long download sends nothing from the client.
func TestRelayKeepsAConnectionOpenWhileOnlyOneDirectionIsActive(t *testing.T) {
	push, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = push.Close() })
	go func() {
		for {
			c, err := push.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				// like many servers, a client that has stopped writing is taken to have left
				go func() { _, _ = io.Copy(io.Discard, c); _ = c.Close() }()
				for i := 0; i < 8; i++ { // longer than the idle time in all, with gaps well inside it
					if _, err := c.Write([]byte("tick")); err != nil {
						return
					}
					time.Sleep(100 * time.Millisecond)
				}
			}()
		}
	}()
	addr := startRelay(t, &Relay{Target: push.Addr().String(), IdleTimeout: 350 * time.Millisecond})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	got, err := io.ReadAll(c)
	if err != nil || string(got) != strings.Repeat("tick", 8) {
		t.Fatalf("the stream was cut short: %q %v", got, err)
	}
}

func TestRelayFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	r, listen, err := RelayFromEnv(env(map[string]string{"DRYDOCK_RELAY_LISTEN": ":8080", "DRYDOCK_RELAY_TARGET": "drydock-t-ws:3000"}))
	if err != nil || listen != ":8080" || r.Target != "drydock-t-ws:3000" {
		t.Fatalf("%+v %q %v", r, listen, err)
	}
	for _, bad := range []map[string]string{
		{},
		{"DRYDOCK_RELAY_LISTEN": ":8080"},
		{"DRYDOCK_RELAY_LISTEN": ":8080", "DRYDOCK_RELAY_TARGET": "ws"},
		{"DRYDOCK_RELAY_LISTEN": ":8080", "DRYDOCK_RELAY_TARGET": ":3000"},
		{"DRYDOCK_RELAY_LISTEN": ":8080", "DRYDOCK_RELAY_TARGET": "ws:0"},
		{"DRYDOCK_RELAY_LISTEN": ":8080", "DRYDOCK_RELAY_TARGET": "ws:70000"},
		{"DRYDOCK_RELAY_LISTEN": "8080", "DRYDOCK_RELAY_TARGET": "ws:3000"},
	} {
		if _, _, err := RelayFromEnv(env(bad)); err == nil {
			t.Errorf("%v should be refused", bad)
		}
	}
}
