package connections

import (
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/agents/manifests"
)

// manifestState says whether the manifest declared a value for a capability: "declared" for true/false/estimate,
// "unknown" for the literal unknown or absence.
func manifestState(m *manifests.Manifest, key string) string {
	switch v := m.Capabilities[key].(type) {
	case bool:
		return "declared"
	case string:
		if v == "estimate" {
			return "declared"
		}
	}
	return "unknown"
}

// fillMatrix completes the report's capability matrix with provenance: observed (the agent said so, or a step showed it),
// declared (the manifest or the ACP protocol baseline), or unknown. Cells already set by a prompt step are kept.
func fillMatrix(r *Report, m *manifests.Manifest, obs *acp.Observed) {
	c := r.Capabilities
	vals := map[string]bool{
		"streaming": c.Streaming, "structured_output": c.StructuredOutput, "model_selection": c.ModelSelection, "effort_control": c.EffortControl,
		"session_resume": c.SessionResume, "interrupt": c.Interrupt, "mcp": c.MCP, "token_usage": c.TokenUsage, "cost_reporting": c.CostReporting,
		"headless": c.Headless, "image_input": c.ImageInput, "tool_call_visibility": c.ToolCallVisibility,
	}
	set := func(k, prov string) {
		if _, done := r.Matrix[k]; !done {
			r.Matrix[k] = Cell{vals[k], prov}
		}
	}
	for k := range vals {
		prov := "unknown"
		if m != nil {
			prov = manifestState(m, k)
		}
		if obs != nil {
			switch k {
			case "session_resume", "image_input":
				prov = "observed"
			case "mcp", "interrupt", "tool_call_visibility", "streaming", "headless":
				prov = "declared" // ACP protocol baselines, confirmed by T3/T4 only
			case "model_selection", "effort_control":
				if r.stepStatus("T2b") == StepPass {
					prov = "observed"
				}
			case "token_usage", "cost_reporting":
				if !obs.Usage { // not seen: the manifest's declaration stands until a prompt shows otherwise
					set(k, prov)
					continue
				}
				prov = "observed"
			}
		}
		set(k, prov)
	}
	if obs != nil {
		r.Matrix["mcp_http"] = Cell{obs.Info.MCPHTTP, "observed"}
		r.Matrix["mcp_sse"] = Cell{obs.Info.MCPSSE, "observed"}
		r.Matrix["session_load"] = Cell{obs.Info.LoadSession, "observed"}
		r.Matrix["session_resume_method"] = Cell{obs.Info.SessionResume(), "observed"}
		r.Matrix["session_fork"] = Cell{obs.Info.Fork, "observed"}
		r.Matrix["session_list"] = Cell{obs.Info.List, "observed"}
		r.Matrix["audio_input"] = Cell{obs.Info.Audio, "observed"}
		r.Matrix["embedded_context"] = Cell{obs.Info.EmbeddedContext, "observed"}
	}
}

// degradedNotes explains in plain language what is missing or different from what the manifest declared.
func degradedNotes(m *manifests.Manifest, r *Report, obs *acp.Observed, sessErr string) []string {
	var out []string
	if sessErr != "" {
		out = append(out, plainSessionErr(sessErr))
	}
	declared := manifests.DeclaredCapabilities(m)
	if obs != nil {
		i := obs.Info
		switch {
		case !i.LoadSession && !i.Resume:
			msg := "Session resume: this agent advertised neither session/resume nor session/load, so Drydock can only start fresh sessions with it."
			if declared.SessionResume {
				msg += " The manifest assumed resume would work; the agent's own report wins."
			}
			out = append(out, msg)
		case !i.Resume && i.LoadSession:
			out = append(out, "Session resume: only session/load is available, which replays the whole history to the client; Drydock suppresses the replay but it can be slow for long sessions.")
		}
		if !i.MCPHTTP {
			out = append(out, "MCP over HTTP: not advertised, so the Drydock gateway is attached through the stdio shim.")
		}
		if !i.Image {
			out = append(out, "Image input: not supported by this agent.")
		}
		if r.stepStatus("T2b") == StepPass {
			if !r.Capabilities.ModelSelection {
				out = append(out, "Model choice: this agent exposes no model list in a session, so its own default model is used and Drydock cannot switch it.")
			}
			if !r.Capabilities.EffortControl {
				out = append(out, "Effort control: this agent exposes no effort setting; Drydock cannot lower it to save tokens.")
			}
		}
		if r.stepStatus("T3") == StepPass {
			if c, ok := r.Matrix["token_usage"]; ok && c.Value == false {
				out = append(out, "Token usage: the agent reported no usage for the test prompt. Drydock records none rather than estimating, so budgets and the quota ledger cannot see this agent's tokens.")
			}
			if c, ok := r.Matrix["cost_reporting"]; ok && c.Value == false {
				out = append(out, "Cost: the agent reported no dollar cost. Drydock shows tokens only for this agent.")
			}
		}
		return out
	}
	var unknown []string
	for _, k := range []string{"model_selection", "effort_control", "session_resume", "interrupt", "mcp", "tool_call_visibility", "image_input"} {
		if manifestState(m, k) == "unknown" {
			unknown = append(unknown, strings.ReplaceAll(k, "_", " "))
		}
	}
	if len(unknown) > 0 {
		// what turns one of these on: a contract test against the real CLI (docs/contract-tests.md), then the manifest declares it
		out = append(out, "Not yet seen working with this CLI, so Drydock does not rely on it: "+strings.Join(unknown, ", ")+
			". A contract test that runs the real CLI (docs/contract-tests.md) verifies it, and the CLI's manifest then declares it; until then Drydock leaves it off.")
	}
	if h := m.Transports.Headless; h != nil && h.Unverified && m.Transports.Primary == manifests.TransportHeadless {
		out = append(out, "The transport for this CLI is marked unverified in its manifest: its protocol has not been captured against a real install.")
	}
	return out
}

func summary(r *Report, c Connection) string {
	var pass, fail, skip int
	for _, s := range r.Steps {
		switch s.Status {
		case StepPass:
			pass++
		case StepFail:
			fail++
		default:
			skip++
		}
	}
	verdict := "passed"
	if !r.OK {
		verdict = "did not pass"
	}
	var b strings.Builder
	name := r.Adapter
	if r.CLIVersion != "" {
		name += " " + r.CLIVersion
	}
	fmt.Fprintf(&b, "%s over %s: Test connection %s (%d passed, %d failed, %d skipped).\n", name, r.Transport, verdict, pass, fail, skip)
	for _, s := range r.Steps {
		line := fmt.Sprintf("  %s %s: %s", s.ID, s.Name, s.Status)
		if s.Reason != "" {
			line += " - " + s.Reason
		} else if s.Detail != "" {
			line += " - " + s.Detail
		}
		b.WriteString(line + "\n")
	}
	if len(r.Degraded) > 0 {
		b.WriteString("What is limited:\n")
		for _, d := range r.Degraded {
			b.WriteString("  - " + d + "\n")
		}
	}
	for _, w := range c.Warnings {
		b.WriteString("Note: " + w + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
