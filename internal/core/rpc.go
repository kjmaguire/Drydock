package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// Method is one callable operation. Both transports (the Wails bridge and the development HTTP server) dispatch through the
// same registry, so the UI sees identical behavior in the app and under test.
type Method func(ctx context.Context, params json.RawMessage) (any, error)

func bind[T any](f func(ctx context.Context, p T) (any, error)) Method {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p T
		if len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
		}
		return f(ctx, p)
	}
}

type idParam struct {
	ID string `json:"id"`
}

// Methods returns the registry of UI-callable operations.
func (c *Core) Methods() map[string]Method {
	base := map[string]Method{
		"app.info": bind(func(ctx context.Context, _ struct{}) (any, error) {
			return map[string]any{"name": "Drydock", "user": c.cfg.User, "human": c.Human(), "dataDir": c.cfg.DataDir}, nil
		}),
		"project.create": bind(func(ctx context.Context, p struct{ Name, RepoPath string }) (any, error) {
			return c.CreateProject(ctx, p.Name, p.RepoPath)
		}),
		"project.setup": bind(func(ctx context.Context, p SetupRequest) (any, error) { return c.SetupProject(ctx, p) }),
		"project.attachRepo": bind(func(ctx context.Context, p struct {
			ID       string `json:"id"`
			RepoPath string `json:"repoPath"`
		}) (any, error) {
			return c.AttachRepository(ctx, p.ID, p.RepoPath)
		}),
		"project.setBrief": bind(func(ctx context.Context, p struct {
			ID    string `json:"id"`
			Brief string `json:"brief"`
		}) (any, error) {
			return c.SetBrief(ctx, p.ID, p.Brief)
		}),
		"project.list": bind(func(ctx context.Context, _ struct{}) (any, error) { return c.Projects(ctx) }),
		"project.get":  bind(func(ctx context.Context, p idParam) (any, error) { return c.GetProject(ctx, p.ID) }),

		"project.update": bind(func(ctx context.Context, p struct {
			ID            string `json:"id"`
			Mode          string `json:"mode"`
			MergeStrategy string `json:"mergeStrategy"`
		}) (any, error) {
			return c.UpdateProject(ctx, p.ID, p.Mode, p.MergeStrategy)
		}),
		"github.status": bind(func(ctx context.Context, _ struct{}) (any, error) { return c.GitHubAccountStatus(ctx), nil }),
		"github.owners": bind(func(ctx context.Context, _ struct{}) (any, error) { return c.GitHubOwners(ctx) }),
		"github.repos": bind(func(ctx context.Context, p struct {
			Owner string `json:"owner"`
			Query string `json:"query"`
		}) (any, error) {
			return c.GitHubRepos(ctx, p.Owner, p.Query)
		}),
		"github.cloneDir": bind(func(ctx context.Context, _ struct{}) (any, error) { return DefaultCloneDir(), nil }),
		"github.clone": bind(func(ctx context.Context, p struct {
			Repo string `json:"repo"`
			Dir  string `json:"dir"`
		}) (any, error) {
			return c.GitHubClone(ctx, p.Repo, p.Dir)
		}),
		"github.issues": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
			Labels    string `json:"labels"`
		}) (any, error) {
			return c.GitHubIssues(ctx, p.ProjectID, p.Labels)
		}),
		"github.importIssue": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
			Number    int    `json:"number"`
		}) (any, error) {
			return c.ImportIssue(ctx, p.ProjectID, p.Number)
		}),
		"github.pullRequest": bind(func(ctx context.Context, p struct {
			TaskID string `json:"taskId"`
		}) (any, error) {
			return c.PullRequest(ctx, p.TaskID)
		}),

		"connection.list": bind(func(ctx context.Context, _ struct{}) (any, error) { return c.Connections.DetectAll(ctx), nil }),
		"connection.acknowledge": bind(func(ctx context.Context, p struct {
			GateID string `json:"gateId"`
		}) (any, error) {
			return nil, c.AcknowledgeGate(ctx, p.GateID)
		}),
		"connection.test": bind(func(ctx context.Context, p struct {
			Adapter string `json:"adapter"`
			Prompt  bool   `json:"prompt"` // also send one tiny prompt on the cheapest model (uses the human's account)
		}) (any, error) {
			return c.TestConnection(ctx, p.Adapter, p.Prompt)
		}),
		"adapter.list": bind(func(ctx context.Context, _ struct{}) (any, error) { return c.DescribeAdapters(ctx), nil }),
		"agent.create": bind(func(ctx context.Context, p AgentSpec) (any, error) { return c.CreateAgent(ctx, p) }),
		"agent.list": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
		}) (any, error) {
			return c.Agents(ctx, p.ProjectID)
		}),

		"task.create": bind(func(ctx context.Context, p tasks.CreateReq) (any, error) { return c.CreateTask(ctx, p) }),
		"task.list": bind(func(ctx context.Context, p struct {
			ProjectID string   `json:"projectId"`
			Statuses  []string `json:"statuses"`
		}) (any, error) {
			f := tasks.Filter{ProjectID: p.ProjectID, Limit: 500}
			for _, s := range p.Statuses {
				f.Statuses = append(f.Statuses, tasks.Status(s))
			}
			return c.Tasks.List(ctx, f)
		}),
		"task.get":  bind(func(ctx context.Context, p idParam) (any, error) { return c.GetTask(ctx, p.ID) }),
		"task.diff": bind(func(ctx context.Context, p idParam) (any, error) { return c.TaskDiff(ctx, p.ID) }),
		"task.assign": bind(func(ctx context.Context, p struct{ ID, Assignee, Reviewer string }) (any, error) {
			return c.AssignTask(ctx, p.ID, p.Assignee, p.Reviewer)
		}),
		"task.start":  bind(func(ctx context.Context, p idParam) (any, error) { return c.StartTask(ctx, p.ID) }),
		"task.cancel": bind(func(ctx context.Context, p idParam) (any, error) { return nil, c.CancelTask(ctx, p.ID) }),

		"run.respond": bind(func(ctx context.Context, p struct {
			RunID, RequestID, Decision string
			Data                       json.RawMessage
		}) (any, error) {
			return nil, c.Respond(ctx, p.RunID, p.RequestID, p.Decision, p.Data)
		}),

		"room.list": bind(func(ctx context.Context, p struct {
			ProjectID       string `json:"projectId"`
			IncludeArchived bool   `json:"includeArchived"`
		}) (any, error) {
			return c.Room.ListRooms(ctx, p.ProjectID, p.IncludeArchived)
		}),
		"room.get": bind(func(ctx context.Context, p idParam) (any, error) { return c.Room.GetRoom(ctx, p.ID) }),
		"room.thread": bind(func(ctx context.Context, p struct {
			RootID string `json:"rootId"`
		}) (any, error) {
			return c.Room.Thread(ctx, p.RootID)
		}),
		"room.createDM": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
			AgentID   string `json:"agentId"`
		}) (any, error) {
			return c.Room.CreateDM(ctx, p.ProjectID, room.Human(c.cfg.User), room.Agent(p.AgentID))
		}),
		"room.members": bind(func(ctx context.Context, p struct {
			RoomID string `json:"roomId"`
		}) (any, error) {
			return c.Room.Members(ctx, p.RoomID)
		}),
		"room.messages": bind(func(ctx context.Context, p struct {
			RoomID   string `json:"roomId"`
			AfterSeq int64  `json:"afterSeq"`
			Limit    int    `json:"limit"`
		}) (any, error) {
			return c.Room.Messages(ctx, p.RoomID, p.AfterSeq, p.Limit)
		}),
		"room.post": bind(func(ctx context.Context, p struct {
			RoomID, Body, ThreadRoot string
		}) (any, error) {
			return c.PostMessage(ctx, p.RoomID, p.Body, p.ThreadRoot)
		}),
		"room.markRead": bind(func(ctx context.Context, p struct {
			RoomID string `json:"roomId"`
			Seq    int64  `json:"seq"`
		}) (any, error) {
			return c.Room.MarkRead(ctx, p.RoomID, room.Human(c.cfg.User), p.Seq)
		}),
		"room.unread": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
		}) (any, error) {
			return c.Room.UnreadCounts(ctx, room.Human(c.cfg.User), p.ProjectID)
		}),
		"needsyou.list": bind(func(ctx context.Context, p struct {
			ProjectID string `json:"projectId"`
		}) (any, error) {
			return c.Room.NeedsYou(ctx, p.ProjectID)
		}),
		"presence.list": bind(func(ctx context.Context, _ struct{}) (any, error) { return c.Presence.Snapshot(), nil }),

		"events.head": bind(func(ctx context.Context, _ struct{}) (any, error) { return c.Bus.LastSeq(ctx) }),
		"usage.list": bind(func(ctx context.Context, p struct {
			TaskID  string `json:"taskId"`
			AgentID string `json:"agentId"`
			Limit   int    `json:"limit"`
		}) (any, error) {
			return c.Usage(ctx, p.TaskID, p.AgentID, p.Limit)
		}),
		"agent.get": bind(func(ctx context.Context, p idParam) (any, error) { return c.GetAgent(ctx, p.ID) }),

		"provider.list": bind(func(ctx context.Context, _ struct{}) (any, error) { return c.Providers.List(ctx) }),
		"provider.create": bind(func(ctx context.Context, p providers.NewProvider) (any, error) {
			return c.Providers.Create(ctx, p)
		}),
		"provider.delete": bind(func(ctx context.Context, p idParam) (any, error) { return nil, c.Providers.Delete(ctx, p.ID) }),
		"provider.setEnabled": bind(func(ctx context.Context, p struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		}) (any, error) {
			return nil, c.Providers.SetEnabled(ctx, p.ID, p.Enabled)
		}),
		"provider.test":   bind(func(ctx context.Context, p idParam) (any, error) { return c.Prober.Probe(ctx, p.ID) }),
		"provider.models": bind(func(ctx context.Context, p idParam) (any, error) { return c.Providers.Models(ctx, p.ID) }),

		"events.replay": bind(func(ctx context.Context, p struct {
			After uint64   `json:"after"`
			Types []string `json:"types"`
			Task  string   `json:"taskId"`
			Limit int      `json:"limit"`
		}) (any, error) {
			return c.Replay(ctx, p.After, events.Filter{Types: p.Types, TaskID: p.Task}, p.Limit)
		}),
	}
	for k, v := range c.extMethods {
		if _, dup := base[k]; !dup {
			base[k] = v
		}
	}
	c.gateMethods(base) // expired license: methods that start work are refused with a plain message (ext_packaging.go)
	return base
}

// Call dispatches one method by name.
func (c *Core) Call(ctx context.Context, name string, params json.RawMessage) (json.RawMessage, error) {
	m, ok := c.Methods()[name]
	if !ok {
		return nil, fmt.Errorf("unknown method %q", name)
	}
	v, err := m(ctx, params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
