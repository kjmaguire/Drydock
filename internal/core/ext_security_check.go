package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/scan"
)

// The security scan outside the Security page (decision P131): the `scan` check of a task's worktree, and the
// security_findings tool through which an agent reads the findings of its project's last scan.

var securityRegistry sync.Map // *Core -> *securityExt

func (c *Core) security() *securityExt {
	if v, ok := securityRegistry.Load(c); ok {
		return v.(*securityExt)
	}
	return nil
}

// scanCheckOptions is pipeline.Deps.ScanTools: a task's `scan` check uses the tools and rules a scan of its project uses, the
// environment every project command gets, the app's redaction, and the findings a person dismissed in the project.
func (c *Core) scanCheckOptions(ctx context.Context, projectID string) scan.CheckOptions {
	o := scan.CheckOptions{Redact: c.Scanner.Redact, Env: checks.CommandEnv, Paths: c.cfg.ScanTools}
	s := c.security()
	if s == nil {
		return o
	}
	o.Paths, o.BuiltinRules, o.RulesDir = s.paths(ctx), s.builtinRules, s.rulesDir(ctx, projectID)
	o.Dismissed = func(ctx context.Context) map[string]scan.Dismissal {
		d, err := s.dismissals(ctx, projectID)
		if err != nil {
			// a finding a person dismissed then counts: the check fails, it does not pass for want of the list
			c.logf("security: the dismissed findings of %s could not be read for a check: %v", projectID, err)
			return nil
		}
		return d
	}
	return o
}

// mcpSecurity is mcp.Workspace.Security.
type mcpSecurity struct{ c *Core }

// AgentFinding is one finding as an agent reads it: what names and locates it, never a secret's value and no line of code.
// The agent reads the lines in its own worktree with its file tools: the scan read the project's working tree (the person's
// checkout, which may hold changes the person has not committed), and Drydock does not hand that tree's text to a model.
type AgentFinding struct {
	Fingerprint string   `json:"fingerprint"`
	Engine      string   `json:"engine"`
	Severity    string   `json:"severity"`
	Rule        string   `json:"rule"`
	Title       string   `json:"title"`
	File        string   `json:"file,omitempty"`
	Line        int      `json:"line,omitempty"`
	EndLine     int      `json:"endLine,omitempty"`
	Package     string   `json:"package,omitempty"`
	Version     string   `json:"version,omitempty"`
	IDs         []string `json:"ids,omitempty"`
	Fixed       string   `json:"fixed,omitempty"`
	Unfixed     []string `json:"unfixed,omitempty"`
}

// AgentFindings is the security_findings answer.
type AgentFindings struct {
	ScanID     string         `json:"scanId,omitempty"`
	FinishedAt int64          `json:"finishedAt,omitempty"`
	Summary    string         `json:"summary,omitempty"`
	Engines    []AgentEngine  `json:"engines,omitempty"`
	Total      int            `json:"total"`
	Shown      int            `json:"shown"`
	Findings   []AgentFinding `json:"findings"`
	Note       string         `json:"note"`
}

// AgentEngine says whether an engine of the scan gave its findings (done) or not (skipped, errored).
type AgentEngine struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

var severityOrder = map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3, "info": 4}

// Findings reads the run's project's last finished scan with the dismissals as they are now, filtered and capped, redacted
// once more as a whole. A scan in progress is not read: its engines may still be finding.
func (p mcpSecurity) Findings(ctx context.Context, run mcp.RunInfo, q mcp.FindingsQuery) (any, error) {
	s := p.c.security()
	if s == nil {
		return nil, errors.New("the security scan is not running in this build")
	}
	rep, err := s.lastFinished(ctx, run.ProjectID)
	if err != nil {
		return nil, err
	}
	out := AgentFindings{Findings: []AgentFinding{}}
	if rep == nil {
		out.Note = "This project has not been scanned yet. Only a person starts a scan (the project's Security page)."
		return out, nil
	}
	s.overlay(ctx, run.ProjectID, rep)
	out.ScanID, out.FinishedAt, out.Summary = rep.ID, rep.FinishedAt, rep.Summary
	for _, e := range rep.Engines {
		out.Engines = append(out.Engines, AgentEngine{ID: string(e.ID), Status: string(e.Status)})
	}
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	least, ok := severityOrder[q.MinSeverity]
	if !ok {
		least = severityOrder["info"]
	}
	prefix := strings.TrimPrefix(strings.ReplaceAll(q.Path, `\`, "/"), "./")
	for _, f := range rep.Findings {
		if f.Dismissed || (q.Engine != "" && string(f.Engine) != q.Engine) || severityOrder[string(f.Severity)] > least {
			continue
		}
		if prefix != "" && f.File != prefix && !strings.HasPrefix(f.File, strings.TrimSuffix(prefix, "/")+"/") {
			continue
		}
		out.Total++
		if len(out.Findings) < limit {
			out.Findings = append(out.Findings, AgentFinding{Fingerprint: f.Fingerprint, Engine: string(f.Engine), Severity: string(f.Severity), Rule: f.Rule,
				Title: f.Title, File: f.File, Line: f.Line, EndLine: f.EndLine, Package: f.Package, Version: f.Version, IDs: f.IDs, Fixed: f.Fixed, Unfixed: f.Unfixed})
		}
	}
	out.Shown = len(out.Findings)
	out.Note = "Findings of the project's working tree when it was scanned; a line may have moved in your worktree. A person dismisses findings and starts scans."
	if rep.Truncated {
		out.Note += " The scan kept its first findings only."
	}
	// redacted as a whole, as a stored report is: nothing that looks like a secret survives, even in a file name
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(p.c.Scanner.Redact(string(b))), nil
}

// lastFinished is the project's newest finished scan: one the database refused (kept in memory), else the newest stored one.
func (s *securityExt) lastFinished(ctx context.Context, projectID string) (*scan.Report, error) {
	s.mu.Lock()
	kept, held := s.unstored[projectID]
	if held {
		kept = kept.Clone()
	}
	s.mu.Unlock()
	if held {
		return &kept, nil
	}
	var doc string
	err := s.c.DB.Reader().QueryRowContext(ctx, `SELECT report_json FROM security_scans WHERE project_id=? ORDER BY started_at DESC, id DESC LIMIT 1`, projectID).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeReport(doc)
}
