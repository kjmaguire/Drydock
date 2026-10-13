// Package scan is the one-button security scan of a project's working tree (docs/security-scan.md, decision P44): three
// engines run at once over the same directory and their findings are read into one report. Secrets are found with the
// gitleaks rules compiled into Drydock, vulnerable dependencies with osv-scanner, and code weaknesses with Opengrep and a
// rules folder the person chose. A finding never carries a secret's value; what each engine said is kept as identifiers,
// a file and a line. An engine whose tool is not installed is skipped and says so: a skipped engine is never a pass.
package scan

import "time"

// EngineID names an engine.
type EngineID string

// The engines, in the order the report lists them.
const (
	Secrets EngineID = "secrets"
	Deps    EngineID = "deps"
	SAST    EngineID = "sast"
)

// EngineStatus is where an engine is.
type EngineStatus string

const (
	Pending   EngineStatus = "pending"
	Running   EngineStatus = "running"
	Done      EngineStatus = "done"
	Skipped   EngineStatus = "skipped"   // the tool is not installed or has nothing to scan: not a pass
	Errored   EngineStatus = "errored"   // the tool ran and broke, or did not finish in time: not a pass
	Cancelled EngineStatus = "cancelled" // the person cancelled the scan
)

// Engine describes one engine for the screen.
type Engine struct {
	ID   EngineID `json:"id"`
	Name string   `json:"name"`
	Tool string   `json:"tool"`
	// Looks says what the engine looks for, in one sentence.
	Looks string `json:"looks"`
}

// Engines are the three engines in report order.
var Engines = []Engine{
	{ID: Secrets, Name: "Secrets", Tool: "gitleaks", Looks: "Keys, tokens and passwords left in files."},
	{ID: Deps, Name: "Dependencies", Tool: "osv-scanner", Looks: "Packages in the lockfiles and manifests with known vulnerabilities."},
	{ID: SAST, Name: "Code", Tool: "opengrep", Looks: "Code patterns the rules folder marks as unsafe (injection, weak crypto, unsafe deserialisation and the like)."},
}

// EngineResult is one engine's state in a report. While it runs, Done and Total carry its progress when the engine can
// count (the secrets engine counts files); Total 0 means the engine cannot say how far it is.
type EngineResult struct {
	ID     EngineID     `json:"id"`
	Name   string       `json:"name"`
	Tool   string       `json:"tool"`
	Status EngineStatus `json:"status"`
	// Reason says why an engine was skipped, errored or cancelled, in plain words (redacted; never a tool's raw output).
	Reason     string `json:"reason,omitempty"`
	StartedAt  int64  `json:"startedAt,omitempty"`  // unix milliseconds
	FinishedAt int64  `json:"finishedAt,omitempty"` // unix milliseconds
	Done       int    `json:"done,omitempty"`
	Total      int    `json:"total,omitempty"`
	Findings   int    `json:"findings"`
	// Unread counts the folders and files the secrets engine could not read (permissions); they were not scanned, and
	// Reason says so.
	Unread int `json:"unread,omitempty"`
	// Version is what the tool said about itself (--version), first line, capped; empty for an engine that did not run.
	Version string `json:"version,omitempty"`
}

// Severity orders findings. Secrets are high; a vulnerable dependency is high; a code finding takes its rule's level.
type Severity string

const (
	Critical Severity = "critical"
	High     Severity = "high"
	Medium   Severity = "medium"
	Low      Severity = "low"
	Info     Severity = "info"
)

// Finding is one thing an engine found. It locates and names; it never quotes the secret or the vulnerable line.
type Finding struct {
	Engine   EngineID `json:"engine"`
	Severity Severity `json:"severity"`
	// Rule is the engine's rule id (gitleaks rule, Opengrep check id, or the ecosystem for a dependency).
	Rule string `json:"rule"`
	// Title is a short plain-language line (redacted, at most 200 bytes).
	Title string `json:"title"`
	// File is relative to the scanned directory, with forward slashes.
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	EndLine int    `json:"endLine,omitempty"`
	// Package, Version and IDs belong to a dependency finding: the vulnerable package and its advisory ids.
	Package string   `json:"package,omitempty"`
	Version string   `json:"version,omitempty"`
	IDs     []string `json:"ids,omitempty"`
}

// Counts is how many findings of each severity a report has.
type Counts struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Info     int `json:"info"`
	Total    int `json:"total"`
}

// Report status.
const (
	StatusRunning   = "running"
	StatusDone      = "done"
	StatusCancelled = "cancelled"
)

// MaxFindings caps a report: past it, Truncated is set and the rest are counted but not kept.
const MaxFindings = 2000

// Report is one scan of one directory.
type Report struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Dir       string `json:"dir"`
	Status    string `json:"status"`
	// By is who started the scan ("human:<name>").
	By         string         `json:"by"`
	StartedAt  int64          `json:"startedAt"`
	FinishedAt int64          `json:"finishedAt,omitempty"`
	Engines    []EngineResult `json:"engines"`
	Findings   []Finding      `json:"findings"`
	Counts     Counts         `json:"counts"`
	// Summary is one plain sentence once the scan is done ("Scanned in 12 s: 1 secret, 2 vulnerable packages, 5 code findings").
	Summary   string `json:"summary,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	// StoreError is set when the finished report could not be written to the database: it is then kept in memory until
	// the next scan of the project and the page says so.
	StoreError string `json:"storeError,omitempty"`
}

// Summary is a report without its findings, for the history list.
type Summary struct {
	ID         string `json:"id"`
	ProjectID  string `json:"projectId"`
	Status     string `json:"status"`
	By         string `json:"by"`
	StartedAt  int64  `json:"startedAt"`
	FinishedAt int64  `json:"finishedAt,omitempty"`
	Counts     Counts `json:"counts"`
	Summary    string `json:"summary,omitempty"`
}

// ToolInfo says whether a tool is on this machine.
type ToolInfo struct {
	Tool string `json:"tool"`
	// BuiltIn is true for the engine compiled into Drydock (gitleaks).
	BuiltIn bool   `json:"builtIn,omitempty"`
	Found   bool   `json:"found"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
	// Install says how to get the tool when it is missing.
	Install string `json:"install,omitempty"`
}

// Tools is what the Security page shows about this machine and the project's choices.
type Tools struct {
	Gitleaks ToolInfo `json:"gitleaks"`
	OSV      ToolInfo `json:"osv"`
	Opengrep ToolInfo `json:"opengrep"`
	// RulesDir is the folder of Opengrep rules the project uses; RulesOK says it exists and holds at least one .yaml/.yml file.
	RulesDir  string `json:"rulesDir"`
	RulesOK   bool   `json:"rulesOk"`
	RulesNote string `json:"rulesNote,omitempty"`
	// Ready says every engine can run as things stand; otherwise the page says which cannot (the scan still runs the rest).
	Ready bool `json:"ready"`
}

// Timeout is how long one engine may take.
const Timeout = 10 * time.Minute
