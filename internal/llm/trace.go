package llm

import (
	"context"
	"errors"
	"time"
)

// CallTracer receives one record per Wrapper call (decision P113): the local telemetry sink turns it into a span and its token
// and cost metrics. The record is content-free by construction: it carries the call's identity (purpose, provider, models, the
// project, task and run it belongs to), its timing, a closed error class and the usage figures, never a prompt, a completion,
// a tool call or an error message. The one exception is a ContentTracer that asks for content.
type CallTracer interface {
	TraceCall(ctx context.Context, c CallTrace)
}

// ContentTracer is a CallTracer that can ask for the call's content (decision P121): the telemetry sink while a person has
// turned content export on, a separate opt-in. Only then does the Wrapper fill CallTrace.Content, and never for a call its
// secret or data-class guard refused (that content was judged not fit to leave).
type ContentTracer interface {
	CallTracer
	WantsContent() bool
}

// CallContent is what a call was given and what it answered, for content export only. It holds the text as it was sent; the
// tracer redacts it before it is kept anywhere.
type CallContent struct {
	System       []string
	Messages     []Message
	Output       string
	ToolCalls    []ToolCall
	FinishReason string
}

// CallTrace is what a CallTracer is told about one call, whether it succeeded or not.
type CallTrace struct {
	Purpose       string
	Provider      string // the provider asked for
	RequestModel  string
	ResponseModel string // the model that answered (empty when the call failed)
	ServedBy      string // the provider that answered (a fallback's, when Fallback)
	ProjectID     string
	TaskID        string
	RunID         string
	Start, End    time.Time
	// ErrorType is "" for a call that succeeded, otherwise one of the ErrorType values: never the error's text.
	ErrorType string
	Usage     Usage
	Cost      Cost
	CacheHit  bool
	Fallback  bool
	Stream    bool
	// Content is set only for a ContentTracer that wants it (content export, decision P121); nil otherwise.
	Content *CallContent
}

// ErrorType classifies a call's error into a short closed set fit for an attribute value (OpenTelemetry's error.type): the
// guard that refused it, the provider error's kind, or a cancellation.
func ErrorType(err error) string {
	if err == nil {
		return ""
	}
	var (
		se *SecretError
		de *DataClassError
		qe *QuotaError
		ue *UnavailableError
		si *StreamInterruptedError
		ae *APIError
	)
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &se):
		return "secret_blocked"
	case errors.As(err, &de):
		return "data_class_refused"
	case errors.As(err, &si):
		return string(KindStream)
	case errors.As(err, &qe):
		return "quota"
	case errors.As(err, &ue):
		return "unavailable"
	case errors.As(err, &ae):
		if ae.Kind != "" {
			return string(ae.Kind)
		}
		return "api_error"
	}
	return "error"
}

// trace tells the tracer, if there is one, about a finished call.
func (w *Wrapper) trace(ctx context.Context, req Request, start time.Time, stream bool, resp Response, err error) {
	if w.tracer == nil {
		return
	}
	c := CallTrace{Purpose: req.Purpose, Provider: req.Provider, RequestModel: req.Model, ProjectID: req.ProjectID, TaskID: req.TaskID,
		RunID: req.RunID, Start: start, End: w.now(), ErrorType: ErrorType(err), Stream: stream}
	if err == nil {
		c.ResponseModel, c.ServedBy, c.Usage, c.Cost, c.CacheHit, c.Fallback = resp.ModelUsed, resp.Provider, resp.Usage, resp.Cost, resp.CacheHit, resp.Fallback
	}
	if ct, ok := w.tracer.(ContentTracer); ok && ct.WantsContent() && c.ErrorType != "secret_blocked" && c.ErrorType != "data_class_refused" {
		cc := &CallContent{Messages: append([]Message(nil), req.Messages...)}
		for _, b := range req.System {
			cc.System = append(cc.System, b.Text)
		}
		if err == nil {
			cc.Output, cc.ToolCalls, cc.FinishReason = resp.Content, append([]ToolCall(nil), resp.ToolCalls...), resp.FinishReason
		}
		c.Content = cc
	}
	w.tracer.TraceCall(context.WithoutCancel(ctx), c) // a call cut short is still recorded
}
