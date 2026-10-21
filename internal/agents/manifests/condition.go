package manifests

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Condition is the tiny expression language of auth probes: `exit == 0`, `exit != 1`, or `$.path.to.field == true|false|"text"|123`.
// It is deliberately not a general evaluator.
type Condition struct {
	exit  bool
	path  []string
	neg   bool
	value any
}

var condRe = regexp.MustCompile(`^\s*(exit|\$(?:\.[A-Za-z_][A-Za-z0-9_]*)+)\s*(==|!=)\s*(true|false|-?\d+|"[^"]*")\s*$`)

// ParseCondition parses an expression.
func ParseCondition(s string) (Condition, error) {
	m := condRe.FindStringSubmatch(s)
	if m == nil {
		return Condition{}, fmt.Errorf("unsupported condition syntax (want `exit == N` or `$.field == value`)")
	}
	c := Condition{neg: m[2] == "!="}
	if m[1] == "exit" {
		c.exit = true
	} else {
		c.path = strings.Split(strings.TrimPrefix(m[1], "$."), ".")
	}
	switch lit := m[3]; {
	case lit == "true":
		c.value = true
	case lit == "false":
		c.value = false
	case strings.HasPrefix(lit, `"`):
		c.value = strings.Trim(lit, `"`)
	default:
		var n float64
		_ = json.Unmarshal([]byte(lit), &n)
		c.value = n
	}
	if c.exit {
		if _, ok := c.value.(float64); !ok {
			return Condition{}, fmt.Errorf("exit compares with a number")
		}
	}
	return c, nil
}

// EvalExit evaluates an `exit` condition.
func (c Condition) EvalExit(code int) (bool, error) {
	if !c.exit {
		return false, fmt.Errorf("not an exit condition")
	}
	eq := float64(code) == c.value.(float64)
	return eq != c.neg, nil
}

// EvalJSON evaluates a `$.field` condition against a JSON document. ok is false when the field is absent or the document is
// not JSON, which callers treat as "unknown", never as a failure to authenticate.
func (c Condition) EvalJSON(doc []byte) (result, ok bool) {
	if c.exit {
		return false, false
	}
	var v any
	if json.Unmarshal(doc, &v) != nil {
		return false, false
	}
	for _, k := range c.path {
		m, isMap := v.(map[string]any)
		if !isMap {
			return false, false
		}
		if v, ok = m[k]; !ok {
			return false, false
		}
	}
	eq := fmt.Sprint(v) == fmt.Sprint(c.value)
	return eq != c.neg, true
}
