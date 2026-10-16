// Package anthropic is the llm.Client for the Anthropic Messages API on github.com/anthropics/anthropic-sdk-go.
// It places cache_control markers on the stable prefix (docs/token-control.md s4: tools, then system, then messages),
// reads cache read and write tokens from usage, and leaves retry, Retry-After and quota to the llm.Wrapper.
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/providers"
)

// maxBreakpoints is the Messages API limit on cache_control markers per request.
const maxBreakpoints = 4

// Config tunes the client.
type Config struct {
	// CacheTTL1h asks for the 1 hour cache lifetime (write costs 2x instead of 1.25x, MP D). Default 5 minutes.
	CacheTTL1h bool
	// DefaultMaxTokens is used when a request leaves MaxTokens at 0 (the API requires it). Default 4096.
	DefaultMaxTokens int
}

// Client talks to the Anthropic API for one provider.
type Client struct {
	c   sdk.Client
	p   providers.Provider
	cfg Config
}

// New builds a Client for p with the default Config (it is an llm.BackendFactory).
func New(p providers.Provider, apiKey string, hc *http.Client) (llm.Client, error) {
	return NewWithConfig(p, apiKey, hc, Config{})
}

// NewWithConfig builds a Client with an explicit Config.
func NewWithConfig(p providers.Provider, apiKey string, hc *http.Client, cfg Config) (llm.Client, error) {
	if cfg.DefaultMaxTokens <= 0 {
		cfg.DefaultMaxTokens = 4096
	}
	opts := []option.RequestOption{
		option.WithoutEnvironmentDefaults(), // never pick up ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN or a profile from the environment
		option.WithBaseURL(strings.TrimRight(p.BaseURL, "/")),
		option.WithMaxRetries(0),
	}
	if hc != nil {
		opts = append(opts, option.WithHTTPClient(hc))
	}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	return &Client{c: sdk.NewClient(opts...), p: p, cfg: cfg}, nil
}

func (c *Client) cacheControl() sdk.CacheControlEphemeralParam {
	cc := sdk.NewCacheControlEphemeralParam()
	if c.cfg.CacheTTL1h {
		cc.TTL = sdk.CacheControlEphemeralTTLTTL1h
	}
	return cc
}

func (c *Client) params(r llm.Request) (sdk.MessageNewParams, error) {
	maxTok := r.MaxTokens
	if maxTok <= 0 {
		maxTok = c.cfg.DefaultMaxTokens
	}
	params := sdk.MessageNewParams{Model: sdk.Model(r.Model), MaxTokens: int64(maxTok)}

	// Breakpoints: count them all, drop the earliest beyond the API limit (the later ones cover the longer prefixes).
	total := 0
	for _, b := range r.System {
		if b.Cache {
			total++
		}
	}
	for _, m := range r.Messages {
		if m.Cache {
			total++
		}
	}
	skip := total - maxBreakpoints
	take := func() bool {
		if skip > 0 {
			skip--
			return false
		}
		return true
	}

	for _, b := range r.System {
		tb := sdk.TextBlockParam{Text: b.Text}
		if b.Cache && take() {
			tb.CacheControl = c.cacheControl()
		}
		params.System = append(params.System, tb)
	}

	var msgs []sdk.MessageParam
	push := func(role sdk.MessageParamRole, blocks ...sdk.ContentBlockParamUnion) {
		if n := len(msgs); n > 0 && msgs[n-1].Role == role { // consecutive same-role messages merge (tool results arrive as separate llm messages)
			msgs[n-1].Content = append(msgs[n-1].Content, blocks...)
			return
		}
		msgs = append(msgs, sdk.MessageParam{Role: role, Content: blocks})
	}
	for _, m := range r.Messages {
		switch m.Role {
		case "assistant":
			var blocks []sdk.ContentBlockParamUnion
			if m.Content != "" {
				blocks = append(blocks, sdk.NewTextBlock(m.Content))
			}
			for _, tc := range m.ToolCalls {
				var in any = json.RawMessage(`{}`)
				if strings.TrimSpace(tc.Arguments) != "" {
					if !json.Valid([]byte(tc.Arguments)) {
						return params, errors.New("tool call arguments are not valid JSON")
					}
					in = json.RawMessage(tc.Arguments)
				}
				blocks = append(blocks, sdk.NewToolUseBlock(tc.ID, in, tc.Name))
			}
			if len(blocks) == 0 {
				continue
			}
			// m.Reasoning is dropped: replaying thinking needs the provider's signature.
			push(sdk.MessageParamRoleAssistant, blocks...)
		case "tool":
			push(sdk.MessageParamRoleUser, sdk.NewToolResultBlock(m.ToolCallID, m.Content, false))
		default:
			push(sdk.MessageParamRoleUser, sdk.NewTextBlock(m.Content))
		}
		if m.Cache && take() && len(msgs) > 0 {
			setCache(&msgs[len(msgs)-1].Content[len(msgs[len(msgs)-1].Content)-1], c.cacheControl())
		}
	}
	params.Messages = msgs

	for _, t := range r.Tools {
		tp := sdk.ToolParam{Name: t.Name}
		if t.Description != "" {
			tp.Description = sdk.String(t.Description)
		}
		if len(t.Parameters) > 0 {
			var schema map[string]any
			if err := json.Unmarshal(t.Parameters, &schema); err != nil {
				return params, errors.New("tool " + t.Name + ": parameters are not a JSON object")
			}
			is := sdk.ToolInputSchemaParam{ExtraFields: map[string]any{}}
			for k, v := range schema {
				switch k {
				case "type":
				case "properties":
					is.Properties = v
				case "required":
					if arr, ok := v.([]any); ok {
						for _, x := range arr {
							if s, ok := x.(string); ok {
								is.Required = append(is.Required, s)
							}
						}
					}
				default:
					is.ExtraFields[k] = v
				}
			}
			tp.InputSchema = is
		}
		params.Tools = append(params.Tools, sdk.ToolUnionParam{OfTool: &tp})
	}
	switch r.ToolChoice {
	case "":
	case "auto":
		params.ToolChoice = sdk.ToolChoiceUnionParam{OfAuto: &sdk.ToolChoiceAutoParam{}}
	case "required":
		params.ToolChoice = sdk.ToolChoiceUnionParam{OfAny: &sdk.ToolChoiceAnyParam{}}
	case "none":
		params.ToolChoice = sdk.ToolChoiceUnionParam{OfNone: &sdk.ToolChoiceNoneParam{}}
	default:
		params.ToolChoice = sdk.ToolChoiceUnionParam{OfTool: &sdk.ToolChoiceToolParam{Name: r.ToolChoice}}
	}
	if r.Temperature != nil {
		params.Temperature = sdk.Float(*r.Temperature)
	}
	if r.TopP != nil {
		params.TopP = sdk.Float(*r.TopP)
	}
	if r.Effort != "" { // the router only sets an effort for models that accept it; a wrong one is a 400 the caller sees
		params.OutputConfig = sdk.OutputConfigParam{Effort: sdk.OutputConfigEffort(r.Effort)}
	}
	return params, nil
}

func setCache(b *sdk.ContentBlockParamUnion, cc sdk.CacheControlEphemeralParam) {
	switch {
	case b.OfText != nil:
		b.OfText.CacheControl = cc
	case b.OfToolUse != nil:
		b.OfToolUse.CacheControl = cc
	case b.OfToolResult != nil:
		b.OfToolResult.CacheControl = cc
	}
}

// Complete performs a non-streaming Messages call.
func (c *Client) Complete(ctx context.Context, r llm.Request) (llm.Response, error) {
	params, err := c.params(r)
	if err != nil {
		return llm.Response{}, &llm.APIError{Provider: c.p.ID, Kind: llm.KindInvalid, Message: err.Error(), Cause: err}
	}
	var hr *http.Response
	start := time.Now()
	msg, err := c.c.Messages.New(ctx, params, option.WithResponseInto(&hr))
	if err != nil {
		return llm.Response{}, c.fail(err, hr)
	}
	resp := c.response(msg)
	resp.Latency = time.Since(start)
	resp.EffortApplied = r.Effort != ""
	if hr != nil {
		resp.Header = hr.Header
	}
	return resp, nil
}

// Stream performs a streaming Messages call.
func (c *Client) Stream(ctx context.Context, r llm.Request, fn func(llm.Delta) error) (llm.Response, error) {
	params, err := c.params(r)
	if err != nil {
		return llm.Response{}, &llm.APIError{Provider: c.p.ID, Kind: llm.KindInvalid, Message: err.Error(), Cause: err}
	}
	var hr *http.Response
	start := time.Now()
	stream := c.c.Messages.NewStreaming(ctx, params, option.WithResponseInto(&hr))
	defer func() { _ = stream.Close() }()
	var msg sdk.Message
	toolIdx := map[int64]int{} // content block index -> tool call ordinal
	for stream.Next() {
		ev := stream.Current()
		if err := msg.Accumulate(ev); err != nil {
			return llm.Response{}, &llm.APIError{Provider: c.p.ID, Kind: llm.KindServer, Message: "stream accumulate: " + err.Error(), Cause: err}
		}
		switch v := ev.AsAny().(type) {
		case sdk.ContentBlockStartEvent:
			if v.ContentBlock.Type == "tool_use" {
				toolIdx[v.Index] = len(toolIdx)
				if err := fn(llm.Delta{ToolCall: &llm.ToolCallDelta{Index: toolIdx[v.Index], ID: v.ContentBlock.ID, Name: v.ContentBlock.Name}}); err != nil {
					return llm.Response{}, err
				}
			}
		case sdk.ContentBlockDeltaEvent:
			var d llm.Delta
			switch dv := v.Delta.AsAny().(type) {
			case sdk.TextDelta:
				d.Text = dv.Text
			case sdk.ThinkingDelta:
				d.Reasoning = dv.Thinking
			case sdk.InputJSONDelta:
				d.ToolCall = &llm.ToolCallDelta{Index: toolIdx[v.Index], Arguments: dv.PartialJSON}
			}
			if d != (llm.Delta{}) {
				if err := fn(d); err != nil {
					return llm.Response{}, err
				}
			}
		}
	}
	if err := stream.Err(); err != nil {
		if ctx.Err() != nil {
			return llm.Response{}, ctx.Err()
		}
		return llm.Response{}, c.fail(err, hr)
	}
	resp := c.response(&msg)
	resp.Latency = time.Since(start)
	resp.EffortApplied = r.Effort != ""
	if hr != nil {
		resp.Header = hr.Header
	}
	return resp, nil
}

func (c *Client) response(m *sdk.Message) llm.Response {
	resp := llm.Response{ModelUsed: string(m.Model), UsageReported: true}
	for _, b := range m.Content {
		switch b.Type {
		case "text":
			resp.Content += b.Text
		case "thinking":
			resp.Reasoning += b.Thinking
		case "tool_use":
			args := string(b.Input)
			if args == "" {
				args = "{}"
			}
			resp.ToolCalls = append(resp.ToolCalls, llm.ToolCall{ID: b.ID, Name: b.Name, Arguments: args})
		}
	}
	switch m.StopReason {
	case sdk.StopReasonEndTurn:
		resp.FinishReason = "stop"
	case sdk.StopReasonToolUse:
		resp.FinishReason = "tool_calls"
	case sdk.StopReasonMaxTokens:
		resp.FinishReason = "length"
	default:
		resp.FinishReason = string(m.StopReason)
	}
	u := m.Usage
	// input_tokens excludes cached tokens on this API; the normalized Input includes them (D12).
	resp.Usage = llm.Usage{
		Input:      int(u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens),
		Output:     int(u.OutputTokens),
		Cached:     int(u.CacheReadInputTokens),
		CacheWrite: int(u.CacheCreationInputTokens),
	}
	return resp
}

func (c *Client) fail(err error, hr *http.Response) error {
	var api *sdk.Error
	if errors.As(err, &api) {
		var h http.Header
		if api.Response != nil {
			h = api.Response.Header
		} else if hr != nil {
			h = hr.Header
		}
		return llm.ClassifyHTTP(c.p.ID, api.StatusCode, h, []byte(api.RawJSON()), time.Now())
	}
	s := err.Error()
	var h http.Header
	if hr != nil {
		h = hr.Header
	}
	switch { // in-stream error events carry no HTTP status
	case strings.Contains(s, "overloaded_error"):
		return llm.ClassifyHTTP(c.p.ID, 529, h, nil, time.Now())
	case strings.Contains(s, "rate_limit_error"):
		return llm.ClassifyHTTP(c.p.ID, 429, h, nil, time.Now())
	case strings.Contains(s, "received error while streaming"):
		return &llm.APIError{Provider: c.p.ID, Kind: llm.KindServer, Message: s, Cause: err, Header: h}
	}
	return &llm.APIError{Provider: c.p.ID, Kind: llm.KindNetwork, Message: s, Cause: err, Header: h}
}
