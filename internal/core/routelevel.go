package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/router"
	"github.com/kjmaguire/drydock/internal/storage"
)

// The routing level of agent turns (docs/model-routing.md s3, decision P118). The workspace has one level, Balanced unless a
// person chose another on the Routing page; a project may set its own on its settings page, and its turns then run at that
// level. Each decision records the level it was made at and where the level came from, and "Why this model?" says so. Both
// are settings rows: route.level, and route.level:<project id> (removed with the project; a project export leaves it out,
// as it leaves out where the project's agents run).
//
// Since decision P127 a task has a level of its own too (tasks.route_level, set by a person on the task page) and a workflow
// template may declare one (pipeline.Template.Level). The most specific wins: the task's own, then its template's, then the
// project's, then the workspace's. The template sits under the task and over the project because a task chooses its
// template: the template says what kind of work this task is (a security fix), which the project's level, a default for all
// of its work, does not; a person who wants otherwise for one task sets the task's own level.

const (
	settingLevel        = "route.level"
	settingProjectLevel = "route.level:"
)

// RouteLevel is route.level.get: the workspace's level, the project's own ("" when it follows the workspace) and the level
// the project's turns run at.
type RouteLevel struct {
	ProjectID string `json:"projectId,omitempty"`
	Workspace string `json:"workspace"`
	Project   string `json:"project"`
	// TaskID, Task, Template and TemplateLevel are set when a task was asked about (P127): the task's own level ("" when it
	// follows the rest), its template's title and the level the template declares ("" for none).
	TaskID        string   `json:"taskId,omitempty"`
	Task          string   `json:"task,omitempty"`
	Template      string   `json:"template,omitempty"`
	TemplateLevel string   `json:"templateLevel,omitempty"`
	Effective     string   `json:"effective"`
	Source        string   `json:"source"` // router.LevelFromTask, LevelFromTemplate, LevelFromProject or LevelFromWorkspace
	Levels        []string `json:"levels"`
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

// taskLevel is the task's own level and the name of its template ("" for none or no task).
func (e *economics) taskLevel(ctx context.Context, taskID string) (router.Level, string) {
	if taskID == "" {
		return "", ""
	}
	var level, tpl string
	if err := e.c.DB.Reader().QueryRowContext(ctx, `SELECT route_level, template FROM tasks WHERE id=?`, taskID).Scan(&level, &tpl); err != nil {
		return "", ""
	}
	if !router.ValidLevel(router.Level(level)) {
		level = ""
	}
	return router.Level(level), tpl
}

// templateLevel is the level the template that runs a turn declares: the workflow the turn runs in (either variant), else
// the task's template. "" and "" when it declares none.
func templateLevel(workflow, taskTemplate string) (router.Level, string) {
	for _, name := range []string{strings.TrimSuffix(workflow, pipeline.PRSuffix), taskTemplate} {
		if name == "" {
			continue
		}
		if t, ok := pipeline.TemplateByName(name); ok {
			if router.ValidLevel(router.Level(t.Level)) {
				return router.Level(t.Level), t.Title
			}
			return "", t.Title
		}
	}
	return "", ""
}

// levelFor is the level a turn of the project runs at, and where it came from: the task's own level, its template's, the
// project's, the workspace's (P118, P127). tpl names the template when the level is its.
func (e *economics) levelFor(ctx context.Context, projectID, taskID, workflow string) (level router.Level, from, tpl string) {
	own, taskTpl := e.taskLevel(ctx, taskID)
	if own != "" {
		return own, router.LevelFromTask, ""
	}
	if l, title := templateLevel(workflow, taskTpl); l != "" {
		return l, router.LevelFromTemplate, title
	}
	if l := e.projectLevel(ctx, projectID); l != "" {
		return l, router.LevelFromProject, ""
	}
	return e.workspaceLevel(ctx), router.LevelFromWorkspace, ""
}

func (e *economics) routeLevel(ctx context.Context, projectID, taskID string) (RouteLevel, error) {
	if taskID != "" {
		t, err := e.c.Tasks.Get(ctx, taskID)
		if err != nil {
			return RouteLevel{}, err
		}
		projectID = t.ProjectID
	}
	eff, src, _ := e.levelFor(ctx, projectID, taskID, "")
	rl := RouteLevel{ProjectID: projectID, Workspace: string(e.workspaceLevel(ctx)), Project: string(e.projectLevel(ctx, projectID)), Effective: string(eff), Source: src}
	if taskID != "" {
		own, tplName := e.taskLevel(ctx, taskID)
		rl.TaskID, rl.Task = taskID, string(own)
		l, title := templateLevel("", tplName)
		rl.Template, rl.TemplateLevel = title, string(l)
	}
	for _, l := range router.Levels {
		rl.Levels = append(rl.Levels, string(l))
	}
	return rl, nil
}

// setTaskLevel sets a task's own level ("" follows its template, project and workspace again). Only a person may.
func (e *economics) setTaskLevel(ctx context.Context, taskID, level, by string) (RouteLevel, error) {
	t, err := e.c.Tasks.Get(ctx, taskID)
	if err != nil {
		return RouteLevel{}, err
	}
	if err := e.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE tasks SET route_level=?, updated_at=? WHERE id=?`, level, storage.Now(), taskID)
		return err
	}); err != nil {
		return RouteLevel{}, err
	}
	e.c.publishTask(ctx, "routing.level_changed", t, "", map[string]any{"projectId": t.ProjectID, "taskId": taskID, "level": level, "by": by})
	return e.routeLevel(ctx, "", taskID)
}

// setRouteLevel sets the workspace's level (projectID "") or a project's own; a project's "" makes it follow the workspace
// again. With taskID it sets that task's own level instead. Only a person may.
func (e *economics) setRouteLevel(ctx context.Context, projectID, taskID, level, by string) (RouteLevel, error) {
	if !strings.HasPrefix(by, "human:") || len(by) == len("human:") {
		return RouteLevel{}, errors.New("only a person may change the routing level")
	}
	level = strings.TrimSpace(level)
	if level != "" && !router.ValidLevel(router.Level(level)) {
		return RouteLevel{}, fmt.Errorf("%q is not a level: choose cheapest, fast, balanced, quality, maximum_quality or custom", level)
	}
	if taskID != "" {
		return e.setTaskLevel(ctx, taskID, level, by)
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
	return e.routeLevel(ctx, projectID, "")
}
