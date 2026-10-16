package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// RunShim is the body of `drydock.exe mcp-shim` (docs/mcp.md section 2, transport A): an MCP server on stdio that forwards
// tools/list and tools/call to the run's gateway endpoint over loopback HTTP with the bearer token. getenv supplies
// DRYDOCK_MCP_URL and DRYDOCK_MCP_TOKEN (the CLI passes them from the env block of its MCP config). The shim runs in the CLI's own
// process tree, so the CLI's Job Object kill covers it. It never writes anything but protocol frames to out. It refuses
// to send the token to a non-loopback URL.
func RunShim(ctx context.Context, in io.ReadCloser, out io.WriteCloser, getenv func(string) string) error {
	u, tok := getenv(EnvMCPURL), getenv(EnvMCPToken)
	if err := checkShimTarget(u, tok); err != nil {
		return err
	}
	s := &shim{url: u, token: tok}
	defer s.close()
	srv := sdk.NewServer(&sdk.Implementation{Name: "drydock-shim", Version: "1"}, &sdk.ServerOptions{
		Capabilities: &sdk.ServerCapabilities{Tools: &sdk.ToolCapabilities{}},
	})
	srv.AddReceivingMiddleware(s.middleware)
	return srv.Run(ctx, &sdk.IOTransport{Reader: in, Writer: out})
}

func checkShimTarget(raw, token string) error {
	if token == "" {
		return fmt.Errorf("mcp-shim: %s is not set", EnvMCPToken)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || !isLoopbackHost(u.Hostname()) || !strings.HasPrefix(u.Path, "/mcp/") {
		return fmt.Errorf("mcp-shim: %s must be a loopback http URL of the form http://127.0.0.1:<port>/mcp/<run_id>", EnvMCPURL)
	}
	return nil
}

type shim struct {
	url, token string
	mu         sync.Mutex
	sess       *sdk.ClientSession
}

func (s *shim) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sess != nil {
		_ = s.sess.Close()
		s.sess = nil
	}
}

func (s *shim) session(ctx context.Context) (*sdk.ClientSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sess != nil {
		return s.sess, nil
	}
	hc := &http.Client{
		Transport:     &authTransport{base: http.DefaultTransport, header: "Authorization", value: "Bearer " + s.token},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") },
	}
	c := sdk.NewClient(&sdk.Implementation{Name: "drydock-shim", Version: "1"}, nil)
	sess, err := c.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: s.url, HTTPClient: hc, DisableStandaloneSSE: true, MaxRetries: 1}, nil)
	if err != nil {
		return nil, err
	}
	s.sess = sess
	safe.Go("mcp.shim.session", func() {
		_ = sess.Wait()
		s.mu.Lock()
		if s.sess == sess {
			s.sess = nil
		}
		s.mu.Unlock()
	})
	return sess, nil
}

func (s *shim) drop(sess *sdk.ClientSession) {
	s.mu.Lock()
	if s.sess == sess {
		s.sess = nil
	}
	s.mu.Unlock()
	safe.Go("mcp.shim.drop", func() { _ = sess.Close() })
}

func (s *shim) middleware(next sdk.MethodHandler) sdk.MethodHandler {
	return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
		switch method {
		case "tools/list":
			for attempt := 0; ; attempt++ {
				sess, err := s.session(ctx)
				if err == nil {
					var all []*sdk.Tool
					cursor := ""
					for i := 0; i < 20; i++ {
						var res *sdk.ListToolsResult
						res, err = sess.ListTools(ctx, &sdk.ListToolsParams{Cursor: cursor})
						if err != nil {
							break
						}
						all = append(all, res.Tools...)
						if res.NextCursor == "" {
							break
						}
						cursor = res.NextCursor
					}
					if err == nil {
						if all == nil {
							all = []*sdk.Tool{}
						}
						return &sdk.ListToolsResult{Cacheable: sdk.Cacheable{TTLMs: 0, CacheScope: "private"}, Tools: all}, nil // see Run.middleware
					}
					s.drop(sess)
				}
				if attempt >= 1 || ctx.Err() != nil {
					return nil, fmt.Errorf("gateway unreachable: %w", err)
				}
			}
		case "tools/call":
			cr, ok := req.(*sdk.CallToolRequest)
			if !ok || cr.Params == nil {
				return nil, errors.New("bad tools/call request")
			}
			// A call is retried only when the connection could not be established: once the request was sent it is never
			// repeated, because the tool may be write-class.
			var sess *sdk.ClientSession
			var err error
			for attempt := 0; attempt < 2; attempt++ {
				if sess, err = s.session(ctx); err == nil || ctx.Err() != nil {
					break
				}
			}
			if err != nil {
				return completeResult(cr, textResult("gateway unreachable: "+err.Error(), true)), nil
			}
			res, err := sess.CallTool(ctx, &sdk.CallToolParams{Name: cr.Params.Name, Arguments: cr.Params.Arguments})
			if err == nil {
				return completeResult(cr, res), nil
			}
			var je *jsonrpc.Error
			if errors.As(err, &je) {
				return nil, je
			}
			s.drop(sess)
			return completeResult(cr, textResult("gateway unreachable: "+err.Error(), true)), nil
		}
		return next(ctx, method, req)
	}
}
