// Package secretadapt adapts security.Scanner to the scanner contracts of the packages that must not import
// internal/security (internal/llm and internal/mcp define their own Scanner and Finding types). It is a leaf package:
// only the wiring layer imports it.
package secretadapt

import (
	"context"

	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/security"
)

type llmScanner struct{ s security.SecretScanner }

// LLM returns the llm.Scanner view of s. A scan error is returned to the Wrapper, which fails closed.
func LLM(s security.SecretScanner) llm.Scanner { return llmScanner{s} }

func (a llmScanner) Scan(ctx context.Context, text string) ([]llm.Finding, error) {
	fs, err := a.s.Scan(ctx, text)
	if err != nil {
		return nil, err
	}
	out := make([]llm.Finding, 0, len(fs))
	for _, f := range fs {
		out = append(out, llm.Finding{Rule: f.RuleID})
	}
	return out, nil
}

type mcpScanner struct{ s security.SecretScanner }

// MCP returns the mcp.Scanner view of s. mcp.Scanner has no error result, so a scan failure (size cap, cancelled
// context) is reported as one finding that spans the whole text: the gateway then redacts everything (fail closed).
func MCP(s security.SecretScanner) mcp.Scanner { return mcpScanner{s} }

func (a mcpScanner) Scan(text string) []mcp.Finding {
	fs, err := a.s.Scan(context.Background(), text)
	if err != nil {
		return []mcp.Finding{{Rule: "scan-error", Start: 0, End: len(text)}}
	}
	out := make([]mcp.Finding, 0, len(fs))
	for _, f := range fs {
		start, end := f.Start, f.End
		if end <= start { // offsets unknown: cover the whole text rather than leak
			start, end = 0, len(text)
		}
		out = append(out, mcp.Finding{Rule: f.RuleID, Start: start, End: end})
	}
	return out
}
