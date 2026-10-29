package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/llm/probe"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/router"
)

// Routing settings a person sets on the Routing page (decision P112): the LLM tiebreak (off by default, with its own daily
// budget and a timeout) and the wall-time ceilings of a turn, per level and per agent (none by default). They are stored as
// two JSON settings rows; route.settings.get reads them with what the page shows beside them, route.settings.set changes them.

const (
	settingTiebreak = "route.tiebreak"
	settingCeilings = "route.ceilings"

	// DefaultTiebreakBudgetUSD is the tiebreak's own budget per day (UTC) when the person sets none.
	DefaultTiebreakBudgetUSD = 0.25
	// DefaultTiebreakTimeout bounds one tiebreak call; the ranking's choice stands when it runs out.
	DefaultTiebreakTimeout = 8 * time.Second
	// MaxTiebreakCallsPerDay bounds the calls when their cost is not known (a model without a price): the dollar budget alone
	// could not.
	MaxTiebreakCallsPerDay = 500
	// tiebreakPurpose is the purpose of the call in model_usage (and its cache key).
	tiebreakPurpose = "route_tiebreak"
)

// TiebreakSettings are the tiebreak's settings and what the page shows beside them.
type TiebreakSettings struct {
	Enabled   bool    `json:"enabled"`
	BudgetUSD float64 `json:"budgetUsd"` // per day, UTC
	TimeoutMS int     `json:"timeoutMs"`
	// read only
	Margin        float64 `json:"margin,omitempty"`
	Model         string  `json:"model,omitempty"` // provider/model a tiebreak calls now; "" when no cheap model is selected
	ModelNote     string  `json:"modelNote,omitempty"`
	SpentTodayUSD float64 `json:"spentTodayUsd"`
	CallsToday    int     `json:"callsToday"`
}

// CeilingSettings are the wall-time ceilings of a turn in seconds: per level and per agent (an agent's own wins). Absent or 0:
// no ceiling.
type CeilingSettings struct {
	Levels map[string]int `json:"levels"`
	Agents map[string]int `json:"agents"`
}

// RouteSettings is route.settings.get.
type RouteSettings struct {
	Tiebreak TiebreakSettings `json:"tiebreak"`
	Ceilings CeilingSettings  `json:"ceilings"`
	// Level is the level agent turns run at today (no per-project level exists yet), so the page can say which ceiling applies.
	Level string `json:"level"`
	// Agents are the agents a ceiling can be set for (every project's team), with their project's name.
	Agents []RouteAgent `json:"agents"`
}

// RouteAgent is an agent as the Routing page names it.
type RouteAgent struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Project string `json:"project"`
}

// Ceiling bounds (seconds): shorter than a minute stops any real turn; longer than a day is no ceiling.
const (
	minCeiling = 60
	maxCeiling = 24 * 60 * 60
)

var knownLevels = map[string]bool{string(router.LevelCheapest): true, string(router.LevelFast): true, string(router.LevelBalanced): true,
	string(router.LevelQuality): true, string(router.LevelMaximumQuality): true, string(router.LevelCustom): true}

func (e *economics) tiebreakSettings(ctx context.Context) TiebreakSettings {
	ts := TiebreakSettings{BudgetUSD: DefaultTiebreakBudgetUSD, TimeoutMS: int(DefaultTiebreakTimeout.Milliseconds())}
	if v, _ := e.setting(ctx, settingTiebreak); v != "" {
		_ = json.Unmarshal([]byte(v), &ts)
	}
	if ts.TimeoutMS <= 0 {
		ts.TimeoutMS = int(DefaultTiebreakTimeout.Milliseconds())
	}
	return TiebreakSettings{Enabled: ts.Enabled, BudgetUSD: ts.BudgetUSD, TimeoutMS: ts.TimeoutMS}
}

func (e *economics) ceilingSettings(ctx context.Context) CeilingSettings {
	cs := CeilingSettings{}
	if v, _ := e.setting(ctx, settingCeilings); v != "" {
		_ = json.Unmarshal([]byte(v), &cs)
	}
	if cs.Levels == nil {
		cs.Levels = map[string]int{}
	}
	if cs.Agents == nil {
		cs.Agents = map[string]int{}
	}
	return cs
}

func (e *economics) routeSettings(ctx context.Context) RouteSettings {
	ts := e.tiebreakSettings(ctx)
	ts.Margin = router.TiebreakMargin
	if p, m, note := e.tiebreakModel(ctx); m != "" {
		ts.Model = p + "/" + m
	} else {
		ts.ModelNote = note
	}
	ts.SpentTodayUSD, ts.CallsToday = e.tiebreakSpentToday(ctx)
	rs := RouteSettings{Tiebreak: ts, Ceilings: e.ceilingSettings(ctx), Level: string(router.LevelBalanced), Agents: []RouteAgent{}}
	if ags, err := e.c.AllAgents(ctx); err == nil {
		names := map[string]string{}
		if ps, err := e.c.Projects(ctx); err == nil {
			for _, p := range ps {
				names[p.ID] = p.Name
			}
		}
		for _, a := range ags {
			rs.Agents = append(rs.Agents, RouteAgent{ID: a.ID, Name: a.Name, Project: names[a.ProjectID]})
		}
	}
	return rs
}

// routeSettingsPatch is route.settings.set: a part left out stays as it is; a ceilings map given replaces the stored one.
type routeSettingsPatch struct {
	Tiebreak *struct {
		Enabled   *bool    `json:"enabled"`
		BudgetUSD *float64 `json:"budgetUsd"`
		TimeoutMS *int     `json:"timeoutMs"`
	} `json:"tiebreak"`
	Ceilings *struct {
		Levels map[string]int `json:"levels"`
		Agents map[string]int `json:"agents"`
	} `json:"ceilings"`
}

func (e *economics) setRouteSettings(ctx context.Context, p routeSettingsPatch) (RouteSettings, error) {
	if p.Tiebreak != nil {
		ts := e.tiebreakSettings(ctx)
		if v := p.Tiebreak.Enabled; v != nil {
			ts.Enabled = *v
		}
		if v := p.Tiebreak.BudgetUSD; v != nil {
			if math.IsNaN(*v) || *v < 0 || *v > 10 {
				return RouteSettings{}, errors.New("the tiebreak's daily budget is between $0 and $10")
			}
			ts.BudgetUSD = *v
		}
		if v := p.Tiebreak.TimeoutMS; v != nil {
			if *v < 1000 || *v > 60000 {
				return RouteSettings{}, errors.New("the tiebreak's timeout is between 1 and 60 seconds")
			}
			ts.TimeoutMS = *v
		}
		b, _ := json.Marshal(ts)
		if err := e.putSetting(ctx, settingTiebreak, string(b)); err != nil {
			return RouteSettings{}, err
		}
	}
	if p.Ceilings != nil {
		cs := e.ceilingSettings(ctx)
		if p.Ceilings.Levels != nil {
			cs.Levels = map[string]int{}
			for l, sec := range p.Ceilings.Levels {
				if !knownLevels[l] {
					return RouteSettings{}, fmt.Errorf("%q is not a level", l)
				}
				if err := checkCeiling(sec); err != nil {
					return RouteSettings{}, fmt.Errorf("%s: %w", l, err)
				}
				if sec > 0 {
					cs.Levels[l] = sec
				}
			}
		}
		if p.Ceilings.Agents != nil {
			cs.Agents = map[string]int{}
			for id, sec := range p.Ceilings.Agents {
				a, err := e.c.agent(ctx, id)
				if err != nil {
					return RouteSettings{}, fmt.Errorf("agent %s: %w", id, err)
				}
				if err := checkCeiling(sec); err != nil {
					return RouteSettings{}, fmt.Errorf("%s: %w", a.Name, err)
				}
				if sec > 0 {
					cs.Agents[id] = sec
				}
			}
		}
		b, _ := json.Marshal(cs)
		if err := e.putSetting(ctx, settingCeilings, string(b)); err != nil {
			return RouteSettings{}, err
		}
	}
	e.c.publish(ctx, "routing.settings_changed", "", map[string]any{"by": e.c.Human()})
	return e.routeSettings(ctx), nil
}

func checkCeiling(sec int) error {
	if sec != 0 && (sec < minCeiling || sec > maxCeiling) {
		return fmt.Errorf("a wall-time ceiling is between 1 minute and 24 hours (0 for none), not %d seconds", sec)
	}
	return nil
}

// ceilingFor is the wall-time ceiling of a turn of s: its agent's own, else its level's; 0 for none. from says which.
func (e *economics) ceilingFor(ctx context.Context, s turnSubject) (time.Duration, string) {
	cs := e.ceilingSettings(ctx)
	if sec := cs.Agents[s.agent.ID]; sec > 0 {
		return time.Duration(sec) * time.Second, "the agent " + s.agent.Name
	}
	level := string(router.LevelBalanced) // the level agent turns run at (decide)
	if sec := cs.Levels[level]; sec > 0 {
		return time.Duration(sec) * time.Second, "the " + level + " level"
	}
	return 0, ""
}

// tiebreakModel is the cheap model a tiebreak calls: the cheap_default of the first enabled private provider that has one
// (cheap providers first), chosen by the cheap model selection (route.cheap.select). note says why there is none.
func (e *economics) tiebreakModel(ctx context.Context) (providerID, model, note string) {
	ps, err := e.c.Providers.List(ctx)
	if err != nil {
		return "", "", "the providers could not be read"
	}
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].Tier == providers.TierCheap && ps[j].Tier != providers.TierCheap })
	for _, p := range ps {
		if !p.Enabled || p.DataClass != providers.DataPrivate {
			continue
		}
		if sel, ok, err := probe.Stored(ctx, e.c.DB, p.ID); err == nil && ok && sel.Default != "" {
			return p.ID, sel.Default, ""
		}
	}
	return "", "", "no cheap model is selected yet: run the cheap model selection below (a tiebreak needs a cheap, private model that passed the tool-call probe)"
}

func (e *economics) tiebreakSpentToday(ctx context.Context) (float64, int) {
	now := time.Now().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).UnixMilli()
	var usd float64
	var n int
	_ = e.c.DB.Reader().QueryRowContext(ctx, `SELECT COALESCE(SUM(cost_usd),0), COUNT(*) FROM model_usage WHERE purpose=? AND ts>=? AND cache_hit=0`,
		tiebreakPurpose, day).Scan(&usd, &n)
	return usd, n
}

// tiebreaker is the router's Tiebreaker (router.Tiebreaker): one call to the configured cheap model through the llm.Wrapper
// (data-class guard, secret scan, quota, response cache and usage rows as for every call of Drydock's own), told the turn's
// shape and the tied candidates' facts and nothing of the work. It is off unless a person turned it on.
type tiebreaker struct{ e *economics }

const tiebreakSystem = `You choose which model takes one turn of an AI coding agent. A deterministic router ranked the options below as tied: same tier, same rule penalties, prices within a few percent or unknown. You are told only the turn's kind, class and weights and each option's facts, never the work itself.
Prefer, in this order: an option that fits the turn's weight (complexity and ambiguity from 0 to 1), one with more remaining quota, one with a larger context, one whose tool calling was probed.
Answer with one JSON object and nothing else: {"choice": "<the key of one option>", "why": "<one short sentence>"}`

func (t tiebreaker) Choose(ctx context.Context, q router.TiebreakQuery) (router.TiebreakAnswer, error) {
	e := t.e
	st := e.tiebreakSettings(ctx)
	if !st.Enabled || e.wrapper == nil {
		return router.TiebreakAnswer{}, router.ErrTiebreakOff
	}
	pid, model, note := e.tiebreakModel(ctx)
	if model == "" {
		return router.TiebreakAnswer{}, errors.New(note)
	}
	by := pid + "/" + model
	if spent, calls := e.tiebreakSpentToday(ctx); spent >= st.BudgetUSD || calls >= MaxTiebreakCallsPerDay {
		return router.TiebreakAnswer{By: by}, fmt.Errorf("the tiebreak's own budget for today is used up ($%.4f of $%.2f, %d calls)", spent, st.BudgetUSD, calls)
	}
	desc, _ := json.Marshal(q) // ids, the project and the agent are not part of it (json:"-")
	zero := 0.0
	cctx, cancel := context.WithTimeout(ctx, time.Duration(st.TimeoutMS)*time.Millisecond)
	defer cancel()
	resp, err := e.wrapper.Complete(cctx, llm.Request{Provider: pid, Model: model, MaxTokens: 200, Temperature: &zero, Purpose: tiebreakPurpose, CacheTTL: time.Hour,
		System:   []llm.Block{{Text: tiebreakSystem}},
		Messages: []llm.Message{{Role: "user", Content: "Options and the turn:\n" + string(desc)}},
		// the description holds routing facts only (kinds, classes, model names and prices): no repository, task or room content
		Manifest:  &llm.Manifest{Entries: []llm.ManifestEntry{{SourceType: "routing_facts", Sensitivity: llm.SensitivitySynthetic}}},
		ProjectID: q.ProjectID, AgentID: q.AgentID, TaskID: q.TaskID})
	if err != nil {
		if cctx.Err() != nil && ctx.Err() == nil {
			return router.TiebreakAnswer{By: by}, fmt.Errorf("no answer within %s", time.Duration(st.TimeoutMS)*time.Millisecond)
		}
		return router.TiebreakAnswer{By: by}, errors.New(clipText(e.c.Scanner.Redact(err.Error()), 200))
	}
	var ans struct {
		Choice string `json:"choice"`
		Why    string `json:"why"`
	}
	text := strings.TrimSpace(resp.Content)
	if i, j := strings.Index(text, "{"), strings.LastIndex(text, "}"); i >= 0 && j > i {
		_ = json.Unmarshal([]byte(text[i:j+1]), &ans)
	}
	if ans.Choice == "" {
		return router.TiebreakAnswer{By: by, CostUSD: resp.Cost.USD}, errors.New("the answer was not the JSON asked for")
	}
	return router.TiebreakAnswer{Key: strings.TrimSpace(ans.Choice), Why: clipText(e.c.Scanner.Redact(ans.Why), 160), By: by, CostUSD: resp.Cost.USD}, nil
}

func clipText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n] + "..."
	}
	return s
}

// dryTiebreaker is the Tiebreaker of route.preview: when the tiebreak is on it says a real decision would ask the cheap model,
// and calls nothing (a preview spends no money).
type dryTiebreaker struct{ e *economics }

func (t dryTiebreaker) Choose(ctx context.Context, q router.TiebreakQuery) (router.TiebreakAnswer, error) {
	if !t.e.tiebreakSettings(ctx).Enabled {
		return router.TiebreakAnswer{}, router.ErrTiebreakOff
	}
	by := ""
	if p, m, _ := t.e.tiebreakModel(ctx); m != "" {
		by = p + "/" + m
	}
	return router.TiebreakAnswer{By: by}, router.ErrTiebreakDryRun
}
