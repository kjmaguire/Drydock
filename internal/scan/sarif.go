package scan

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// sarifLog is the part of a SARIF 2.1.0 log the scan reads: each run's results (rule, level, message, first location) and
// its driver's rules (the default level and a security severity when the tool gives one). Written from the SARIF 2.1.0
// specification and the shape Semgrep-family tools emit (runs[].tool.driver.rules[], runs[].results[]); unknown fields are
// ignored. This repository has no output captured from a real Opengrep run; testdata/semgrep-oss-1.182.0.sarif is a real run of
// Semgrep OSS, the engine Opengrep was forked from (sarif_real_test.go says how it was made).
type sarifLog struct {
	Runs []struct {
		Tool struct {
			Driver struct {
				Name    string `json:"name"`
				Version string `json:"semanticVersion"`
				Rules   []struct {
					ID                   string                 `json:"id"`
					ShortDescription     struct{ Text string }  `json:"shortDescription"`
					DefaultConfiguration struct{ Level string } `json:"defaultConfiguration"`
					Properties           struct {
						SecuritySeverity string `json:"security-severity"`
					} `json:"properties"`
				} `json:"rules"`
			} `json:"driver"`
		} `json:"tool"`
		Results []struct {
			RuleID    string                `json:"ruleId"`
			Level     string                `json:"level"`
			Message   struct{ Text string } `json:"message"`
			Locations []struct {
				Physical struct {
					Artifact struct{ URI string } `json:"artifactLocation"`
					Region   struct {
						StartLine int `json:"startLine"`
						EndLine   int `json:"endLine"`
					} `json:"region"`
				} `json:"physicalLocation"`
			} `json:"locations"`
		} `json:"results"`
	} `json:"runs"`
}

// ParseSARIF reads the results of a SARIF log into findings of engine. dir is the scanned directory: a location inside it
// is made relative to it (forward slashes). redact is applied to every title. The findings are sorted by file and line.
func ParseSARIF(data []byte, engine EngineID, dir string, redact func(string) string) ([]Finding, string, error) {
	var log sarifLog
	if err := json.Unmarshal(data, &log); err != nil {
		return nil, "", fmt.Errorf("the SARIF output is not the expected JSON: %w", err)
	}
	if redact == nil {
		redact = func(s string) string { return s }
	}
	var out []Finding
	version := ""
	for _, run := range log.Runs {
		if version == "" {
			version = cleanLine(run.Tool.Driver.Version, 40)
		}
		type ruleMeta struct{ level, sev, title string }
		rules := map[string]ruleMeta{}
		for _, r := range run.Tool.Driver.Rules {
			rules[r.ID] = ruleMeta{level: r.DefaultConfiguration.Level, sev: r.Properties.SecuritySeverity, title: r.ShortDescription.Text}
		}
		for _, res := range run.Results {
			meta := rules[res.RuleID]
			f := Finding{Engine: engine, Rule: cleanLine(res.RuleID, 160), Severity: sarifSeverity(res.Level, meta.level, meta.sev)}
			title := firstLine(res.Message.Text)
			if title == "" {
				title = firstLine(meta.title)
			}
			f.Title = cleanLine(redact(title), 200)
			if len(res.Locations) > 0 {
				loc := res.Locations[0].Physical
				f.File = relativeURI(loc.Artifact.URI, dir)
				f.Line = loc.Region.StartLine
				if loc.Region.EndLine > f.Line {
					f.EndLine = loc.Region.EndLine
				}
			}
			if f.Rule == "" && f.Title == "" {
				continue
			}
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out, version, nil
}

// sarifSeverity maps a result's level (else its rule's default level) to a severity; a rule's security-severity score
// (CVSS-like, 0-10) wins when it is present.
func sarifSeverity(level, ruleLevel, score string) Severity {
	if score != "" {
		if v, err := strconv.ParseFloat(strings.TrimSpace(score), 64); err == nil {
			switch {
			case v >= 9:
				return Critical
			case v >= 7:
				return High
			case v >= 4:
				return Medium
			case v > 0:
				return Low
			}
		}
	}
	if level == "" {
		level = ruleLevel
	}
	switch strings.ToLower(level) {
	case "error":
		return High
	case "warning":
		return Medium
	case "note":
		return Low
	}
	return Info
}

// relativeURI turns a SARIF artifact URI into a path relative to dir with forward slashes: "file:///C:/repo/a.go" or
// "a.go" or "./a.go" all give "a.go".
func relativeURI(uri, dir string) string {
	p := uri
	if strings.HasPrefix(strings.ToLower(uri), "file:") {
		if u, err := url.Parse(uri); err == nil {
			p = u.Path
			if len(p) >= 3 && p[0] == '/' && p[2] == ':' { // /C:/repo
				p = p[1:]
			}
		}
	} else if un, err := url.PathUnescape(uri); err == nil {
		p = un
	}
	p = filepath.ToSlash(p)
	if dir != "" {
		d := filepath.ToSlash(dir)
		if rel, ok := strings.CutPrefix(p, strings.TrimSuffix(d, "/")+"/"); ok {
			p = rel
		}
	}
	p = strings.TrimPrefix(p, "./")
	return cleanLine(p, 300)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return s
}

// cleanLine drops control characters and caps the text at max bytes (on a rune boundary, with an ellipsis).
func cleanLine(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			if r == '\t' {
				b.WriteByte(' ')
			}
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if len(out) <= max {
		return out
	}
	cut := max - 1
	for cut > 0 && !isRuneStart(out[cut]) {
		cut--
	}
	return out[:cut] + "…"
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
