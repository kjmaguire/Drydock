package core_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/packs"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/skills"
)

// watchFixture stands in for GitHub as the registry sync sees it: GET /repos/<owner>/<name>/commits/HEAD answers the
// default branch's commit and GET /<owner>/<name>/tar.gz/<sha> the tarball at that commit. The real service was never
// contacted from here. Registries the fixture does not hold answer 404 (the sync records that as the registry's error).
type watchFixture struct {
	*httptest.Server
	mu    sync.Mutex
	head  map[string]string            // "owner/name" -> sha
	trees map[string]map[string]string // "owner/name@sha" -> path -> content
}

func newWatchFixture(t *testing.T) *watchFixture {
	f := &watchFixture{head: map[string]string{}, trees: map[string]map[string]string{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		switch {
		case len(parts) == 5 && parts[0] == "repos" && parts[3] == "commits":
			sha, ok := f.head[parts[1]+"/"+parts[2]]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"sha":%q}`, sha)
		case len(parts) == 4 && parts[2] == "tar.gz":
			tree, ok := f.trees[parts[0]+"/"+parts[1]+"@"+parts[3]]
			if !ok {
				http.NotFound(w, r)
				return
			}
			var buf bytes.Buffer
			zw := gzip.NewWriter(&buf)
			tw := tar.NewWriter(zw)
			var names []string
			for n := range tree {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				tw.WriteHeader(&tar.Header{Name: parts[1] + "-" + parts[3] + "/" + n, Mode: 0o644, Size: int64(len(tree[n])), Typeflag: tar.TypeReg})
				tw.Write([]byte(tree[n]))
			}
			tw.Close()
			zw.Close()
			w.Header().Set("Content-Type", "application/x-gzip")
			w.Write(buf.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *watchFixture) publish(repo, sha string, files map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.head[repo] = sha
	f.trees[repo+"@"+sha] = files
}

func (f *watchFixture) use(a *app) {
	pf := packs.NewFetcher(packs.FetchPolicy{AllowedHosts: []string{"127.0.0.1"}, NoRobots: true, AllowLoopback: true, RPS: 1000, MaxBytes: 1 << 20, Timeout: 5 * time.Second}, nil)
	a.c.Knowledge().Skills.Fetcher = &skills.GitHubFetcher{Fetcher: pf, APIBase: f.URL, TarballBase: f.URL}
}

const (
	watchSha1 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	watchSha2 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// Decisions P46 and P48: the scheduled pass fills the library from every enabled registry; a project adds from the library
// (accepted at once); a later pass tells the project, once per upstream commit, what is new in the library and which of its
// skills are behind: a notice and a Needs-you item in its alerts channel, and an event. Nothing changes in the project by itself.
func TestTheLibraryFillsFromTheRegistriesAndTheProjectIsToldWhatIsNew(t *testing.T) {
	a := open(t, "")
	p := gitProject(t, a, "cfapp")
	f := newWatchFixture(t)
	f.publish("cloudflare/skills", watchSha1, map[string]string{
		"skills/wrangler/SKILL.md":        fixtureSkill("wrangler", "", "Use the project's Wrangler version."),
		"skills/durable-objects/SKILL.md": fixtureSkill("durable-objects", "", "One object per room."),
	})
	f.use(a)

	// the pass syncs every enabled registry: the one the fixture holds fills, the others record their failure and go on
	sts, err := a.c.RunSkillWatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	st := statusOf(sts, "cloudflare-skills")
	if st.LastCommit != watchSha1 || st.Skills != 2 || st.Blocked != 0 || len(st.Added) != 2 || st.CheckedAt == nil || st.LastError != "" {
		t.Fatalf("%+v", st)
	}
	if other := statusOf(sts, "vercel-agent-skills"); other.LastError == "" || other.Skills != 0 {
		t.Fatalf("an unreachable registry records its failure: %+v", other)
	}
	var lib []skills.LibrarySkill
	a.call("skill.libraryList", map[string]any{"registryId": "cloudflare-skills"}, &lib)
	if len(lib) != 2 || lib[0].Name != "durable-objects" || lib[1].Name != "wrangler" || lib[0].Status != skills.LibraryAvailable || lib[0].Repo != "cloudflare/skills" {
		t.Fatalf("library: %+v", lib)
	}
	alerts := a.alertsRoom(p.ID)
	if n := len(skillNotices(a.roomMessages(alerts))); n != 0 {
		t.Fatalf("a project without skills from the registry is not told: %d notices", n)
	}

	// the project chooses one skill: it arrives accepted, with the person recorded
	var k skills.Skill
	a.call("skill.libraryAdd", map[string]any{"projectId": p.ID, "registryId": "cloudflare-skills", "name": "wrangler"}, &k)
	if k.Status != skills.StatusAccepted || k.Origin != skills.OriginRegistry || k.PinnedCommit != watchSha1 || !strings.HasPrefix(k.DecidedBy, "human:") {
		t.Fatalf("%+v", k)
	}
	var res skills.LibraryAddAllResult
	a.call("skill.libraryAddAll", map[string]any{"projectId": p.ID, "registryId": "cloudflare-skills"}, &res)
	if strings.Join(res.Added, ",") != "durable-objects" || strings.Join(res.Unchanged, ",") != "wrangler" {
		t.Fatalf("%+v", res)
	}

	// upstream adds one skill and changes one: the next pass tells the project once
	f.publish("cloudflare/skills", watchSha2, map[string]string{
		"skills/wrangler/SKILL.md":        fixtureSkill("wrangler", "", "Use the project's Wrangler version. Never guess flags."),
		"skills/durable-objects/SKILL.md": fixtureSkill("durable-objects", "", "One object per room."),
		"skills/queues/SKILL.md":          fixtureSkill("queues", "", "Ack explicitly."),
	})
	sts, err = a.c.RunSkillWatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	st = statusOf(sts, "cloudflare-skills")
	if st.LastCommit != watchSha2 || len(st.Added) != 1 || st.Added[0].Name != "queues" || len(st.Changed) != 1 || st.Changed[0].Name != "wrangler" {
		t.Fatalf("%+v", st)
	}
	if up := st.Projects[p.ID].Updates; len(up) != 1 || up[0].Name != "wrangler" {
		t.Fatalf("the project's wrangler is behind: %+v", st.Projects)
	}
	msgs := a.roomMessages(alerts)
	notices := skillNotices(msgs)
	if len(notices) != 1 || !strings.Contains(notices[0].Body, "1 new skill in the Library (queues)") || !strings.Contains(notices[0].Body, "1 changed upstream (wrangler)") ||
		!strings.Contains(notices[0].Body, "1 of this project's skills is behind (wrangler)") {
		t.Fatalf("notice: %+v", notices)
	}
	var needsYou int
	for _, m := range msgs {
		if m.Type == room.TypeQuestion && strings.HasPrefix(m.Body, "Skill updates from cloudflare/skills") {
			needsYou++
		}
	}
	if needsYou != 1 {
		t.Fatalf("one Needs-you item: %+v", msgs)
	}
	evs, _ := a.c.Replay(context.Background(), 0, events.Filter{Types: []string{skills.EventRegistryUpdates}}, 10)
	if len(evs) != 1 {
		t.Fatalf("events: %+v", evs)
	}
	var data struct {
		Registry string                    `json:"registry"`
		Commit   string                    `json:"commit"`
		Projects map[string]map[string]int `json:"projects"`
	}
	_ = json.Unmarshal(evs[0].Data, &data)
	if data.Registry != "cloudflare-skills" || data.Commit != watchSha2 || data.Projects[p.ID]["added"] != 1 || data.Projects[p.ID]["updates"] != 1 {
		t.Fatalf("event: %s", evs[0].Data)
	}
	// the project copy did not move
	a.call("skill.get", map[string]any{"projectId": p.ID, "name": "wrangler"}, &k)
	if k.PinnedCommit != watchSha1 {
		t.Fatalf("a sync changed the project's copy: %+v", k)
	}

	// told once: another pass at the same commit says nothing again
	if _, err := a.c.RunSkillWatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(skillNotices(a.roomMessages(alerts))); n != 1 {
		t.Fatalf("told twice: %d notices", n)
	}

	// the person updates: the status follows
	a.call("skill.libraryAdd", map[string]any{"projectId": p.ID, "registryId": "cloudflare-skills", "name": "wrangler"}, &k)
	if k.PinnedCommit != watchSha2 || k.Status != skills.StatusAccepted {
		t.Fatalf("%+v", k)
	}
	var fresh []skills.RegistryStatus // a fresh value: json.Unmarshal into the old slice would keep the old Projects map entries
	a.call("skill.registryStatus", map[string]any{}, &fresh)
	if len(statusOf(fresh, "cloudflare-skills").Projects[p.ID].Updates) != 0 {
		t.Fatalf("still behind after the update: %+v", statusOf(fresh, "cloudflare-skills"))
	}
}

// Decision P49: an agent reading a skill's on-demand file is recorded as a use, and the usage report counts it.
func TestAnAgentOpeningASkillFileIsRecordedAsAUse(t *testing.T) {
	a := open(t, "")
	p := gitProject(t, a, "cfapp")
	f := newWatchFixture(t)
	f.publish("cloudflare/skills", watchSha1, map[string]string{"skills/wrangler/SKILL.md": fixtureSkill("wrangler", "", "Use the project's Wrangler version.")})
	f.use(a)
	if _, err := a.c.RunSkillWatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	var k skills.Skill
	a.call("skill.libraryAdd", map[string]any{"projectId": p.ID, "registryId": "cloudflare-skills", "name": "wrangler"}, &k)

	publish := func(name, file string) {
		data, _ := json.Marshal(map[string]any{"kind": "tool_call", "data": map[string]any{"name": name, "args": map[string]any{"file_path": file}}})
		if _, err := a.c.Bus.Publish(context.Background(), events.Event{Type: "agent.output", ProjectID: p.ID, TaskID: "task_1", AgentID: "ag_1", RunID: "arun_1", Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	publish("Read", `D:\wt\task\.claude\drydock-skills\wrangler.md`)     // Windows path, absolute
	publish("Read", skills.OnDemandDir+"/wrangler.md")                   // relative
	publish("Read", "src/index.ts")                                      // an ordinary file
	publish("Write", skills.OnDemandDir+"/wrangler.md")                  // not a read
	publish("Read", "/home/u/wt/.claude/drydock-skills/../secrets/x.md") // not in the folder
	deadline := time.Now().Add(5 * time.Second)
	var uses []events.Event
	for time.Now().Before(deadline) {
		uses, _ = a.c.Replay(context.Background(), 0, events.Filter{Types: []string{skills.EventUsed}}, 10)
		if len(uses) >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(uses) != 2 {
		t.Fatalf("uses recorded: %d", len(uses))
	}
	var us []skills.SkillUsage
	a.call("skill.usage", map[string]any{"projectId": p.ID}, &us)
	if len(us) != 1 || us[0].Name != "wrangler" || us[0].Opened != 2 || us[0].LastUsed == nil {
		t.Fatalf("%+v", us)
	}
}

func statusOf(sts []skills.RegistryStatus, id string) skills.RegistryStatus {
	for _, st := range sts {
		if st.RegistryID == id {
			return st
		}
	}
	return skills.RegistryStatus{}
}

func (a *app) roomMessages(roomID string) []room.Message {
	a.t.Helper()
	var msgs []room.Message
	a.call("room.messages", map[string]any{"roomId": roomID, "afterSeq": 0, "limit": 100}, &msgs)
	return msgs
}

func skillNotices(msgs []room.Message) []room.Message {
	var out []room.Message
	for _, m := range msgs {
		if m.Type == room.TypeSystem && strings.HasPrefix(m.Body, "Skill updates from") {
			out = append(out, m)
		}
	}
	return out
}

var _ = core.Project{}
