package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/router"
)

// The routing level of agent turns (docs/model-routing.md s3, decision P118). The workspace has one level, Balanced unless a
// person chose another on the Routing page; a project may set its own on its settings page, and its turns then run at that
// level. Each decision records the level it was made at and where the level came from, and "Why this model?" says so. Both
// are settings rows: route.level, and route.level:<project id> (removed with the project; a project export leaves it out,
// as it leaves out where the project's agents run).

const (
	settingLevel        = "route.level"
	settingProjectLevel = "route.level:"
)

// RouteLevel is route.level.get: the workspace's level, the project's own ("" when it follows the workspace) and the level
// the project's turns run at.
type RouteLevel struct {
	ProjectID string   `json:"projectId,omitempty"`
	Workspace string   `json:"workspace"`
	Project   string   `json:"project"`
	Effective string   `json:"effective"`
	Source    string   `json:"source"` // router.LevelFromProject or router.LevelFromWorkspace
	Levels    []string `json:"levels"`
}

// workspaceLevel is the level of every project that sets none of its own.
func (e *economics) workspaceLevel(ctx context.Context) router.Level {
	if v, _ := e.setting(ctx, settingLevel); v != "" && router.ValidLevel(router.Level(v)) {
		return router.Level(v)
	}
	return router.LevelBalanced
}

// projectLevel is the project's own level, "" when it follows the workspace.
func (e *economics) projectLevel(ctx context.Context, projectID string) router.Level {
	if projectID == "" {
		return ""
	}
	if v, _ := e.setting(ctx, settingProjectLevel+projectID); v != "" && router.ValidLevel(router.Level(v)) {
		return router.Level(v)
	}
	return ""
}

// levelFor is the level a turn of the project runs at, and where it came from.
func (e *economics) levelFor(ctx context.Context, projectID string) (router.Level, string) {
	if l := e.projectLevel(ctx, projectID); l != "" {
		return l, router.LevelFromProject
	}
	return e.workspaceLevel(ctx), router.LevelFromWorkspace
}

func (e *economics) routeLevel(ctx context.Context, projectID string) RouteLevel {
	eff, src := e.levelFor(ctx, projectID)
	rl := RouteLevel{ProjectID: projectID, Workspace: string(e.workspaceLevel(ctx)), Project: string(e.projectLevel(ctx, projectID)), Effective: string(eff), Source: src}
	for _, l := range router.Levels {
		rl.Levels = append(rl.Levels, string(l))
	}
	return rl
}

// setRouteLevel sets the workspace's level (projectID "") or a project's own; a project's "" makes it follow the workspace
// again. Only a person may.
func (e *economics) setRouteLevel(ctx context.Context, projectID, level, by string) (RouteLevel, error) {
	if !strings.HasPrefix(by, "human:") || len(by) == len("human:") {
		return RouteLevel{}, errors.New("only a person may change the routing level")
	}
	level = strings.TrimSpace(level)
	if level != "" && !router.ValidLevel(router.Level(level)) {
		return RouteLevel{}, fmt.Errorf("%q is not a level: choose cheapest, fast, balanced, quality, maximum_quality or custom", level)
	}
	key := settingLevel
	if projectID != "" {
		if _, err := e.c.GetProject(ctx, projectID); err != nil {
			return RouteLevel{}, err
		}
		key = settingProjectLevel + projectID
	} else if level == "" {
		return RouteLevel{}, errors.New("the workspace needs a level")
	}
	var err error
	if level == "" {
		err = e.deleteSetting(ctx, key)
	} else {
		err = e.putSetting(ctx, key, level)
	}
	if err != nil {
		return RouteLevel{}, err
	}
	e.c.publish(ctx, "routing.level_changed", projectID, map[string]any{"projectId": projectID, "level": level, "by": by})
	return e.routeLevel(ctx, projectID), nil
}
