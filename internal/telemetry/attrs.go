// Package telemetry is Drydock's local telemetry sink (decision D12, brief s10): spans and metrics are written to the local
// SQLite database, only when the human turned telemetry on, with Drydock's own attribute names. "Export" means producing a
// JSON document on demand that the human can read first (Preview) and then save, or, since decision P113, the optional OTLP
// exporter a person points at a collector of their own (otlp.go, written without an OTLP dependency).
//
// What can never be in an attribute: prompts, code, tool output, file contents, secrets. This is enforced structurally, not
// by care: an attribute is stored only if its key is in the allowlist below AND its value has an identifier-shaped form
// (no whitespace, bounded length, nothing the secret scanner recognizes). Free text has no way in.
//
// The one exception is content export (decision P121), a separate switch a person turns on after reading what it means: then,
// and only then, the LLM Wrapper's spans also carry the three content attributes of the OpenTelemetry GenAI conventions
// (contentKeys), each a bounded JSON document with the secret scanner's redaction applied first (internal/redact). Every other
// attribute keeps the identifier rule, and no secret is ever stored.
package telemetry

import (
	"encoding/json"
	"math"
	"regexp"
	"sort"

	"github.com/kjmaguire/drydock/internal/redact"
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

	// AttrFeature names what a usage counter counts: the method of the core the window called (decision P121).
	AttrFeature = "drydock.feature"
)

// The content attributes of the OpenTelemetry GenAI conventions (gen-ai-spans, "Recording content"): JSON documents of the
// messages a model was given, what it answered and its system instructions. Recorded only while content export is on.
const (
	AttrGenAIInputMessages      = "gen_ai.input.messages"
	AttrGenAIOutputMessages     = "gen_ai.output.messages"
	AttrGenAISystemInstructions = "gen_ai.system_instructions"
)

// MaxContentBytes bounds one content attribute (after redaction): a longer document is cut part by part before it is stored.
const MaxContentBytes = 32 << 10

// Metric names.
const (
	MetricTokenUsage = "gen_ai.client.token.usage" // counter, unit {token}
	MetricCostUSD    = "drydock.cost"              // counter, unit USD; only emitted when a cost is known
	MetricNodeTime   = "drydock.node.duration"     // histogram, unit ms
	MetricDropped    = "drydock.telemetry.dropped_attributes"
	MetricLLMTime    = "drydock.llm.duration" // histogram, unit ms: one LLM Wrapper call, retries and waits included
	// MetricFeatureUses is a usage counter (decision P121): how often one feature was used since the last point, by feature name.
	MetricFeatureUses = "drydock.feature.uses" // counter, unit {use}
)

type valueKind int

const (
	kindIdent valueKind = iota // identifier-shaped string
	kindInt
	kindFloat
	kindBool
	kindContent // a bounded JSON document of content, redacted (content export only)
)

// allowed is the closed set of attribute keys that can ever be stored.
var allowed = map[string]valueKind{
	AttrProjectID: kindIdent, AttrTaskID: kindIdent, AttrRunID: kindIdent, AttrNodeID: kindIdent, AttrNodeKind: kindIdent,
	AttrWorkflow: kindIdent, AttrAttempt: kindInt, AttrOutcome: kindIdent, AttrTier: kindIdent, AttrPurpose: kindIdent,
	AttrCostUSD: kindFloat, AttrCostSource: kindIdent, AttrCacheRead: kindInt, AttrCacheWrite: kindInt, AttrCheckKind: kindIdent, AttrCheckPass: kindBool,
	AttrGenAIOperation: kindIdent, AttrGenAIProvider: kindIdent, AttrGenAIRequestModel: kindIdent, AttrGenAIResponseModel: kindIdent,
	AttrGenAITokenType: kindIdent, AttrGenAIInputTokens: kindInt, AttrGenAIOutputTokens: kindInt,
	AttrLLMCacheHit: kindBool, AttrLLMFallback: kindBool, AttrErrorType: kindIdent, AttrFeature: kindIdent,
}

// contentKeys are the attributes content export adds; outside it they are dropped like any key that is not allowed.
var contentKeys = map[string]bool{AttrGenAIInputMessages: true, AttrGenAIOutputMessages: true, AttrGenAISystemInstructions: true}

// ContentKeys returns the content attributes, sorted.
func ContentKeys() []string {
	out := make([]string, 0, len(contentKeys))
	for k := range contentKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
// set, is the secret scanner's Redact: a string value it would change is dropped. Content attributes are dropped (and not
// counted: they are not a mistake, content export is off).
func Sanitize(in map[string]any, redact func(string) string) (clean map[string]any, dropped []string) {
	return sanitize(in, redact, false)
}

// SanitizeContent is Sanitize while content export is on: the content attributes are kept too, as bounded JSON documents
// with redact applied to every string in them (internal/redact.JSON), so a secret is masked, never stored.
func SanitizeContent(in map[string]any, redact func(string) string) (clean map[string]any, dropped []string) {
	return sanitize(in, redact, true)
}

func sanitize(in map[string]any, redactFn func(string) string, content bool) (clean map[string]any, dropped []string) {
	clean = map[string]any{}
	for k, v := range in {
		if contentKeys[k] {
			if !content {
				continue
			}
			if val, ok := contentValue(v, redactFn); ok {
				clean[k] = val
			} else {
				dropped = append(dropped, k)
			}
			continue
		}
		kind, ok := allowed[k]
		if !ok {
			dropped = append(dropped, k)
			continue
		}
		val, ok := coerce(kind, v, redactFn)
		if !ok {
			dropped = append(dropped, k)
			continue
		}
		clean[k] = val
	}
	sort.Strings(dropped)
	return clean, dropped
}

// contentValue is a content attribute's value: a JSON document of at most MaxContentBytes, redacted first.
func contentValue(v any, redactFn func(string) string) (string, bool) {
	s, ok := v.(string)
	if !ok || s == "" || !json.Valid([]byte(s)) {
		return "", false
	}
	if redactFn != nil {
		s = string(redact.JSON([]byte(s), redactFn))
	}
	if len(s) > MaxContentBytes || !json.Valid([]byte(s)) {
		return "", false
	}
	return s, true
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
