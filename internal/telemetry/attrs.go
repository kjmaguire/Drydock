// Package telemetry is Drydock's local telemetry sink (decision D12, brief s10): spans and metrics are written to the local
// SQLite database, only when the human turned telemetry on, with Drydock's own attribute names. There is no network
// exporter and no OTLP dependency: "export" means producing a JSON document on demand that the human can read first
// (Preview) and then save or hand to a collector themselves.
//
// What can never be in an attribute: prompts, code, tool output, file contents, secrets. This is enforced structurally, not
// by care: an attribute is stored only if its key is in the allowlist below AND its value has an identifier-shaped form
// (no whitespace, bounded length, nothing the secret scanner recognizes). Free text has no way in.
package telemetry

import (
	"math"
	"regexp"
	"sort"
)

// Attribute names. gen_ai.* names follow the OpenTelemetry GenAI conventions where they are stable enough to copy (the
// conventions are in Development, so Drydock owns the constants and does not import a semconv package, D12). Token
// attributes use the normalized view: input includes cached tokens, with cache read and write kept apart.
const (
	AttrProjectID  = "drydock.project.id"
	AttrTaskID     = "drydock.task.id"
	AttrRunID      = "drydock.run.id"
	AttrNodeID     = "drydock.node.id"
	AttrNodeKind   = "drydock.node.kind"
	AttrWorkflow   = "drydock.workflow.name"
	AttrAttempt    = "drydock.node.attempt"
	AttrOutcome    = "drydock.outcome" // ok | error | paused
	AttrTier       = "drydock.tier"
	AttrPurpose    = "drydock.purpose"
	AttrCostUSD    = "drydock.cost.usd"
	AttrCostSource = "drydock.cost.source" // reported | estimated
	AttrCacheRead  = "drydock.usage.cache_read_tokens"
	AttrCacheWrite = "drydock.usage.cache_write_tokens"
	AttrCheckKind  = "drydock.check.kind"
	AttrCheckPass  = "drydock.check.passed"
	// The LLM Wrapper's calls (decision P113): whether the response cache answered, whether a router-approved alternative did.
	AttrLLMCacheHit = "drydock.llm.cache_hit"
	AttrLLMFallback = "drydock.llm.fallback"
	// AttrErrorType is OpenTelemetry's error.type: a closed class (llm.ErrorType), never an error's text.
	AttrErrorType = "error.type"

	AttrGenAIOperation     = "gen_ai.operation.name"
	AttrGenAIProvider      = "gen_ai.provider.name"
	AttrGenAIRequestModel  = "gen_ai.request.model"
	AttrGenAIResponseModel = "gen_ai.response.model"
	AttrGenAITokenType     = "gen_ai.token.type" // input | output
	AttrGenAIInputTokens   = "gen_ai.usage.input_tokens"
	AttrGenAIOutputTokens  = "gen_ai.usage.output_tokens"
)

// Metric names.
const (
	MetricTokenUsage = "gen_ai.client.token.usage" // counter, unit {token}
	MetricCostUSD    = "drydock.cost"              // counter, unit USD; only emitted when a cost is known
	MetricNodeTime   = "drydock.node.duration"     // histogram, unit ms
	MetricDropped    = "drydock.telemetry.dropped_attributes"
	MetricLLMTime    = "drydock.llm.duration" // histogram, unit ms: one LLM Wrapper call, retries and waits included
)

type valueKind int

const (
	kindIdent valueKind = iota // identifier-shaped string
	kindInt
	kindFloat
	kindBool
)

// allowed is the closed set of attribute keys that can ever be stored.
var allowed = map[string]valueKind{
	AttrProjectID: kindIdent, AttrTaskID: kindIdent, AttrRunID: kindIdent, AttrNodeID: kindIdent, AttrNodeKind: kindIdent,
	AttrWorkflow: kindIdent, AttrAttempt: kindInt, AttrOutcome: kindIdent, AttrTier: kindIdent, AttrPurpose: kindIdent,
	AttrCostUSD: kindFloat, AttrCostSource: kindIdent, AttrCacheRead: kindInt, AttrCacheWrite: kindInt, AttrCheckKind: kindIdent, AttrCheckPass: kindBool,
	AttrGenAIOperation: kindIdent, AttrGenAIProvider: kindIdent, AttrGenAIRequestModel: kindIdent, AttrGenAIResponseModel: kindIdent,
	AttrGenAITokenType: kindIdent, AttrGenAIInputTokens: kindInt, AttrGenAIOutputTokens: kindInt,
	AttrLLMCacheHit: kindBool, AttrLLMFallback: kindBool, AttrErrorType: kindIdent,
}

// Allowlist returns the attribute keys that may be stored, sorted (shown by the preview so the human sees the whole
// vocabulary).
func Allowlist() []string {
	out := make([]string, 0, len(allowed))
	for k := range allowed {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// identValue is what a string attribute value must look like: an id, a model name, an enum. No whitespace, so no sentence,
// no code line, no log line; short, so no blob.
var identValue = regexp.MustCompile(`^[A-Za-z0-9._:/@+#-]{1,96}$`)

// Sanitize keeps the allowlisted attributes whose values have the right shape and returns the names it dropped. redact, if
// set, is the secret scanner's Redact: a string value it would change is dropped.
func Sanitize(in map[string]any, redact func(string) string) (clean map[string]any, dropped []string) {
	clean = map[string]any{}
	for k, v := range in {
		kind, ok := allowed[k]
		if !ok {
			dropped = append(dropped, k)
			continue
		}
		val, ok := coerce(kind, v, redact)
		if !ok {
			dropped = append(dropped, k)
			continue
		}
		clean[k] = val
	}
	sort.Strings(dropped)
	return clean, dropped
}

func coerce(kind valueKind, v any, redact func(string) string) (any, bool) {
	switch kind {
	case kindIdent:
		s, ok := v.(string)
		if !ok || !identValue.MatchString(s) {
			return nil, false
		}
		if redact != nil && redact(s) != s {
			return nil, false
		}
		return s, true
	case kindInt:
		switch n := v.(type) {
		case int:
			return int64(n), true
		case int32:
			return int64(n), true
		case int64:
			return n, true
		case float64:
			if n == math.Trunc(n) && !math.IsInf(n, 0) {
				return int64(n), true
			}
		}
	case kindFloat:
		switch n := v.(type) {
		case float64:
			if !math.IsNaN(n) && !math.IsInf(n, 0) {
				return n, true
			}
		case int:
			return float64(n), true
		}
	case kindBool:
		if b, ok := v.(bool); ok {
			return b, true
		}
	}
	return nil, false
}
