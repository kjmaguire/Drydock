package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// fetchRange serves the gateway-native fetch_range tool: a byte window of a result stored earlier in this run. It is
// rate-limited, reads only handles minted for this run, and its output is marked untrusted like any other result.
func (r *Run) fetchRange(ctx context.Context, c *callRec, canon []byte) *sdk.CallToolResult {
	g := r.g
	c.server = "gateway"
	var in struct {
		Handle string   `json:"handle"`
		Offset *float64 `json:"offset"`
		Length *float64 `json:"length"`
	}
	if err := json.Unmarshal(canon, &in); err != nil {
		return textResult("invalid arguments: "+err.Error(), true)
	}
	if !handleRe.MatchString(in.Handle) {
		return textResult("invalid handle: use the handle printed in a truncation footer", true)
	}
	offset, length := 0, 8192
	if in.Offset != nil {
		if *in.Offset < 0 || *in.Offset != float64(int(*in.Offset)) {
			return textResult("offset must be a non-negative integer", true)
		}
		offset = int(*in.Offset)
	}
	if in.Length != nil {
		if *in.Length < 1 || *in.Length != float64(int(*in.Length)) {
			return textResult("length must be a positive integer", true)
		}
		length = int(*in.Length)
	}
	if length > g.opts.FetchRangeMax {
		length = g.opts.FetchRangeMax
	}
	if !r.fetchLim.allow() {
		c.upstreamStatus = "rate_limited"
		return textResult(fmt.Sprintf("fetch_range is rate limited; retry in %s", r.fetchLim.retryAfter().Round(time.Second)), true)
	}
	r.hmu.Lock()
	path, ok := r.handles[in.Handle]
	r.hmu.Unlock()
	if !ok {
		return textResult("unknown handle: handles are valid only for the run that received them", true)
	}
	f, err := os.Open(path)
	if err != nil {
		return textResult("the stored result is no longer available", true)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return textResult("the stored result is no longer available", true)
	}
	total := int(st.Size())
	if offset > total {
		return textResult(fmt.Sprintf("offset %d is beyond the end of the result (%d bytes)", offset, total), true)
	}
	// Read a little past the window so that the end can be moved back to a rune boundary.
	win := make([]byte, minInt(length+4, total-offset))
	if _, err := f.ReadAt(win, int64(offset)); err != nil && err != io.EOF {
		return textResult("reading the stored result failed", true)
	}
	part, start, end := byteRange(string(win), 0, length)
	start += offset
	end += offset
	c.rawBytes = total
	c.handle = in.Handle
	c.upstreamStatus = "n/a"
	c.suspicious = FlagInjection(part)
	text := wrapUntrusted("gateway", c.id, in.Handle, part, c.suspicious)
	if end < total {
		text += fmt.Sprintf("\n[range %d-%d of %d bytes; call fetch_range{handle:%q, offset:%d, length:%d} for the next part]", start, end, total, in.Handle, end, length)
	} else {
		text += fmt.Sprintf("\n[end of result: bytes %d-%d of %d]", start, end, total)
	}
	c.returnedBytes = len(text)
	c.tokens = EstimateTokens(len([]rune(text)))
	c.truncated = end < total
	res := textResult(text, false)
	res.Meta = sdk.Meta{"drydock": map[string]any{"call_id": c.id, "handle": in.Handle, "offset": start, "end": end, "bytes_total": total}}
	return res
}
