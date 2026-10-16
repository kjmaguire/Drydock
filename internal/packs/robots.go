package packs

import (
	"regexp"
	"strings"
)

// Robots is a parsed robots.txt for one host, including the Content-Signal extension
// ("Content-Signal: ai-train=yes, search=yes, ai-input=yes").
type Robots struct {
	groups  []robotsGroup
	signals map[string]string
	// DisallowAll is set when robots.txt could not be fetched because the server failed (5xx): the safe reading.
	DisallowAll bool
}

type robotsGroup struct {
	agents []string
	rules  []robotsRule
}

type robotsRule struct {
	allow bool
	re    *regexp.Regexp
	n     int // specificity: pattern length
}

// ParseRobots parses robots.txt text. Unknown lines are ignored.
func ParseRobots(body string) *Robots {
	// a robots.txt is the site's to write: bytes that are not UTF-8 cannot be compiled into a pattern (regexp refuses them), and
	// a panic there ended the whole app (audit 2026-10-16, H9)
	body = strings.ToValidUTF8(body, "\uFFFD")
	r := &Robots{signals: map[string]string{}}
	var cur *robotsGroup
	lastWasAgent := false
	for _, line := range strings.Split(body, "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "user-agent":
			if cur == nil || !lastWasAgent {
				r.groups = append(r.groups, robotsGroup{})
				cur = &r.groups[len(r.groups)-1]
			}
			cur.agents = append(cur.agents, strings.ToLower(v))
			lastWasAgent = true
			continue
		case "allow", "disallow":
			if cur != nil && v != "" {
				re, err := robotsPattern(v)
				if err != nil {
					// a rule that cannot be read cannot be obeyed: do not crawl a site whose own rules cannot be understood
					r.DisallowAll = true
					continue
				}
				cur.rules = append(cur.rules, robotsRule{allow: k == "allow", re: re, n: len(v)})
			}
		case "content-signal":
			for _, kv := range strings.Split(v, ",") {
				name, val, ok := strings.Cut(strings.TrimSpace(kv), "=")
				if !ok {
					continue
				}
				name, val = strings.ToLower(strings.TrimSpace(name)), strings.ToLower(strings.TrimSpace(val))
				// the most restrictive statement wins if the file contradicts itself
				if old, seen := r.signals[name]; !seen || (old == "yes" && val == "no") {
					r.signals[name] = val
				}
			}
		}
		lastWasAgent = false
	}
	return r
}

func robotsPattern(p string) (*regexp.Regexp, error) {
	anchored := strings.HasSuffix(p, "$")
	p = strings.TrimSuffix(p, "$")
	var b strings.Builder
	b.WriteString("^")
	for _, part := range strings.Split(p, "*") {
		b.WriteString(regexp.QuoteMeta(part))
		b.WriteString(".*")
	}
	s := strings.TrimSuffix(b.String(), ".*")
	if anchored {
		s += "$"
	}
	return compilePattern(s)
}

// compilePattern is regexp.Compile; a variable so a test can make it refuse a pattern.
var compilePattern = regexp.Compile

// Allowed reports whether the agent may fetch path (with query). The group naming the agent wins over "*"; the longest
// matching rule decides and Allow wins a tie.
func (r *Robots) Allowed(agent, path string) bool {
	if r == nil {
		return true
	}
	if r.DisallowAll {
		return false
	}
	agent = strings.ToLower(agent)
	var chosen *robotsGroup
	for i := range r.groups {
		for _, a := range r.groups[i].agents {
			if a != "*" && a != "" && strings.Contains(agent, a) {
				chosen = &r.groups[i]
			}
		}
	}
	if chosen == nil {
		for i := range r.groups {
			for _, a := range r.groups[i].agents {
				if a == "*" {
					chosen = &r.groups[i]
				}
			}
		}
	}
	if chosen == nil {
		return true
	}
	best, allowed := -1, true
	for _, rule := range chosen.rules {
		if rule.re.MatchString(path) && (rule.n > best || (rule.n == best && rule.allow)) {
			best, allowed = rule.n, rule.allow
		}
	}
	return allowed
}

// Signal returns the value of a Content-Signal key ("ai-input" -> "yes"/"no") and whether the file states it at all.
func (r *Robots) Signal(name string) (string, bool) {
	if r == nil {
		return "", false
	}
	v, ok := r.signals[strings.ToLower(name)]
	return v, ok
}
