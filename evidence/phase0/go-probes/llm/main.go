package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	aopt "github.com/anthropics/anthropic-sdk-go/option"
	openai "github.com/openai/openai-go/v3"
	oopt "github.com/openai/openai-go/v3/option"
)

func main() {
	ctx := context.Background()
	// ---------- Anthropic ----------
	var hits int32
	var lastBody string
	asrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		lastBody = string(b)
		n := atomic.AddInt32(&hits, 1)
		fmt.Printf("  [anthropic fake] %s %s attempt=%d retry-count-hdr=%q x-api-key=%q\n", r.Method, r.URL.Path, n, r.Header.Get("X-Stainless-Retry-Count"), r.Header.Get("X-Api-Key"))
		w.Header().Set("anthropic-ratelimit-requests-remaining", "49")
		w.Header().Set("anthropic-ratelimit-input-tokens-remaining", "39000")
		if n == 1 {
			w.Header().Set("retry-after-ms", "10")
			w.WriteHeader(529)
			w.Write([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`))
			return
		}
		if strings.Contains(string(b), `"stream":true`) {
			w.Header().Set("content-type", "text/event-stream")
			fl := w.(http.Flusher)
			ev := func(name, data string) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data); fl.Flush() }
			ev("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-x","content":[],"stop_reason":null,"usage":{"input_tokens":12,"cache_creation_input_tokens":2000,"cache_read_input_tokens":500,"output_tokens":1,"cache_creation":{"ephemeral_5m_input_tokens":2000,"ephemeral_1h_input_tokens":0}}}}`)
			ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
			ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`)
			ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`)
			ev("content_block_stop", `{"type":"content_block_stop","index":0}`)
			ev("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":7}}`)
			ev("message_stop", `{"type":"message_stop"}`)
			return
		}
		w.Header().Set("content-type", "application/json")
		w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-x","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":12,"cache_creation_input_tokens":2000,"cache_read_input_tokens":500,"output_tokens":3}}`))
	}))
	defer asrv.Close()
	ac := anthropic.NewClient(aopt.WithBaseURL(asrv.URL), aopt.WithAPIKey("sk-test"))
	params := anthropic.MessageNewParams{
		Model:     "claude-x",
		MaxTokens: 100,
		System: []anthropic.TextBlockParam{{
			Text:         "You are a coding agent. (long stable prefix)",
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}},
		Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("hi"))},
	}
	var resp *http.Response
	msg, err := ac.Messages.New(ctx, params, aopt.WithResponseInto(&resp))
	fmt.Println("anthropic non-stream err:", err)
	fmt.Printf("  attempts=%d (1 retry after 529)\n", hits)
	fmt.Printf("  usage: in=%d out=%d cache_write=%d cache_read=%d\n", msg.Usage.InputTokens, msg.Usage.OutputTokens, msg.Usage.CacheCreationInputTokens, msg.Usage.CacheReadInputTokens)
	fmt.Printf("  headers: remaining-requests=%s remaining-input-tokens=%s\n", resp.Header.Get("anthropic-ratelimit-requests-remaining"), resp.Header.Get("anthropic-ratelimit-input-tokens-remaining"))
	fmt.Println("  request body contains cache_control marker:", strings.Contains(lastBody, `"cache_control":{"type":"ephemeral"}`))
	// 1h TTL
	p2 := params
	p2.System = []anthropic.TextBlockParam{{Text: "x", CacheControl: anthropic.CacheControlEphemeralParam{TTL: anthropic.CacheControlEphemeralTTLTTL1h}}}
	atomic.StoreInt32(&hits, 1)
	_, _ = ac.Messages.New(ctx, p2)
	fmt.Println("  1h ttl body:", strings.Contains(lastBody, `"ttl":"1h"`))
	// streaming
	atomic.StoreInt32(&hits, 0)
	var sresp *http.Response
	stream := ac.Messages.NewStreaming(ctx, params, aopt.WithResponseInto(&sresp))
	acc := anthropic.Message{}
	text := ""
	for stream.Next() {
		ev := stream.Current()
		_ = acc.Accumulate(ev)
		if d, ok := ev.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
			if t, ok := d.Delta.AsAny().(anthropic.TextDelta); ok {
				text += t.Text
			}
		}
	}
	fmt.Println("anthropic stream err:", stream.Err(), "text:", text)
	fmt.Printf("  stream attempts=%d; accumulated usage: in=%d out=%d cache_write=%d cache_read=%d stop=%s\n", hits, acc.Usage.InputTokens, acc.Usage.OutputTokens, acc.Usage.CacheCreationInputTokens, acc.Usage.CacheReadInputTokens, acc.StopReason)
	fmt.Printf("  stream resp headers: remaining-requests=%s\n", sresp.Header.Get("anthropic-ratelimit-requests-remaining"))

	// ---------- OpenAI (OpenRouter-style baseURL) ----------
	var ohits int32
	var obody string
	osrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		obody = string(b)
		n := atomic.AddInt32(&ohits, 1)
		fmt.Printf("  [openai fake] %s %s attempt=%d bearer=%t\n", r.Method, r.URL.Path, n, strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "))
		w.Header().Set("x-ratelimit-remaining-requests", "99")
		w.Header().Set("x-ratelimit-remaining-tokens", "123456")
		if n == 1 {
			w.Header().Set("retry-after-ms", "10")
			w.WriteHeader(429)
			w.Write([]byte(`{"error":{"message":"rate","type":"rate_limit_error"}}`))
			return
		}
		if strings.Contains(obody, `"stream":true`) {
			w.Header().Set("content-type", "text/event-stream")
			fl := w.(http.Flusher)
			fmt.Fprint(w, `data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"},"finish_reason":null}]}`+"\n\n")
			fl.Flush()
			fmt.Fprint(w, `data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"prompt_tokens":2006,"completion_tokens":5,"total_tokens":2011,"prompt_tokens_details":{"cached_tokens":1920,"cache_write_tokens":80}}}`+"\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			fl.Flush()
			return
		}
		w.Header().Set("content-type", "application/json")
		w.Write([]byte(`{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2006,"completion_tokens":5,"total_tokens":2011,"prompt_tokens_details":{"cached_tokens":1920,"cache_write_tokens":80}}}`))
	}))
	defer osrv.Close()
	oc := openai.NewClient(oopt.WithBaseURL(osrv.URL+"/api/v1/"), oopt.WithAPIKey("or-test"), oopt.WithHeader("HTTP-Referer", "https://example.invalid"), oopt.WithMaxRetries(3))
	var oresp *http.Response
	t0 := time.Now()
	cc, err := oc.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:    "m",
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	}, oopt.WithResponseInto(&oresp))
	fmt.Println("openai non-stream err:", err, "elapsed", time.Since(t0).Round(time.Millisecond))
	fmt.Printf("  attempts=%d usage: prompt=%d completion=%d cached=%d cache_write=%d\n", ohits, cc.Usage.PromptTokens, cc.Usage.CompletionTokens, cc.Usage.PromptTokensDetails.CachedTokens, cc.Usage.PromptTokensDetails.CacheWriteTokens)
	fmt.Printf("  headers: remaining-requests=%s remaining-tokens=%s\n", oresp.Header.Get("x-ratelimit-remaining-requests"), oresp.Header.Get("x-ratelimit-remaining-tokens"))
	atomic.StoreInt32(&ohits, 1)
	var osresp *http.Response
	os_ := oc.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
		Model:         "m",
		Messages:      []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
		StreamOptions: openai.ChatCompletionStreamOptionsParam{IncludeUsage: openai.Bool(true)},
	}, oopt.WithResponseInto(&osresp))
	acc2 := openai.ChatCompletionAccumulator{}
	for os_.Next() {
		acc2.AddChunk(os_.Current())
	}
	fmt.Println("openai stream err:", os_.Err(), "content:", acc2.Choices[0].Message.Content)
	fmt.Printf("  stream usage: prompt=%d cached=%d cache_write=%d; body has include_usage=%v; resp header remaining-tokens=%s\n", acc2.Usage.PromptTokens, acc2.Usage.PromptTokensDetails.CachedTokens, acc2.Usage.PromptTokensDetails.CacheWriteTokens, strings.Contains(obody, `"include_usage":true`), osresp.Header.Get("x-ratelimit-remaining-tokens"))
}
