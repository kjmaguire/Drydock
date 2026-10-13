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

// watchFixture stands in for GitHub as the registry check sees it: GET /repos/<owner>/<name>/commits/HEAD answers the
// default branch's commit and GET /<owner>/<name>/tar.gz/<sha> the tarball at that commit. The real service was never
// contacted from here.
type watchFixture struct {
	*httptest.Server
	mu    sync.Mutex
	head  map[string]string            // "owner/name" -> sha
	trees map[string]map[string]string // "owner/name@sha" -> path -> content
	tars  int
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
			f.tars++
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

// Decision P46: a registry is imported whole from the Skills page, and the scheduled check tells a project, once per
// upstream commit, what is new or changed for it: a notice and a Needs-you item in its alerts channel, and an event.
func TestImportAllThenTheRegistryCheckTellsTheProjectWhatIsNew(t *testing.T) {
	a := open(t, "")
	p := gitProject(t, a, "cfapp")
	f := newWatchFixture(t)
	f.publish("cloudflare/skills", watchSha1, map[string]string{
		"skills/wrangler/SKILL.md":        fixtureSkill("wrangler", "", "Use the project's Wrangler version."),
		"skills/durable-objects/SKILL.md": fixtureSkill("durable-objects", "", "One object per room."),
	})
	f.use(a)

	var res skills.ImportAllResult
	a.call("skill.importAll", map[string]any{"projectId": p.ID, "registryId": "cloudflare-skills"}, &res)
	if res.Commit != watchSha1 || strings.Join(res.Imported, ",") != "durable-objects,wrangler" || len(res.Blocked)+len(res.Skipped) != 0 {
		t.Fatalf("%+v", res)
	}
	var list []skills.Skill
	a.call("skill.list", map[string]any{"projectId": p.ID}, &list)
	if len(list) != 2 || list[0].Status != skills.StatusOffered || list[1].Status != skills.StatusOffered {
		t.Fatalf("every imported skill is offered, none accepted: %+v", list)
	}

	// the first check finds nothing new: no notice
	var sts []skills.RegistryStatus
	a.call("skill.registryCheck", map[string]any{}, &sts)
	st := statusOf(sts, "cloudflare-skills")
	if !st.Watched || st.LastCommit != watchSha1 || st.NotifiedCommit != watchSha1 || st.CheckedAt == nil || len(st.Projects[p.ID].Additions) != 0 {
		t.Fatalf("%+v", st)
	}
	alerts := a.alertsRoom(p.ID)
	if msgs := a.roomMessages(alerts); len(skillNotices(msgs)) != 0 {
		t.Fatalf("a notice for nothing: %+v", msgs)
	}

	// upstream adds one skill and changes one
	f.publish("cloudflare/skills", watchSha2, map[string]string{
		"skills/wrangler/SKILL.md":        fixtureSkill("wrangler", "", "Use the project's Wrangler version. Never guess flags."),
		"skills/durable-objects/SKILL.md": fixtureSkill("durable-objects", "", "One object per room."),
		"skills/queues/SKILL.md":          fixtureSkill("queues", "", "Ack explicitly."),
	})
	sts, err := a.c.RunSkillWatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	st = statusOf(sts, "cloudflare-skills")
	ch := st.Projects[p.ID]
	if st.LastCommit != watchSha2 || len(ch.Additions) != 1 || ch.Additions[0].Name != "queues" || len(ch.Updates) != 1 || ch.Updates[0].Name != "wrangler" {
		t.Fatalf("%+v", st)
	}
	msgs := a.roomMessages(alerts)
	notices := skillNotices(msgs)
	if len(notices) != 1 || !strings.Contains(notices[0].Body, "1 new skill (queues)") || !strings.Contains(notices[0].Body, "1 changed (wrangler)") || !strings.Contains(notices[0].Body, "cloudflare/skills") {
		t.Fatalf("notice: %+v", notices)
	}
	var needsYou []room.Message
	for _, m := range msgs {
		if m.Type == room.TypeQuestion && strings.HasPrefix(m.Body, "Skill updates from cloudflare/skills") {
			needsYou = append(needsYou, m)
		}
	}
	if len(needsYou) != 1 {
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
	if data.Registry != "cloudflare-skills" || data.Commit != watchSha2 || data.Projects[p.ID]["additions"] != 1 || data.Projects[p.ID]["updates"] != 1 {
		t.Fatalf("event: %s", evs[0].Data)
	}

	// told once: another pass at the same commit says nothing again
	if _, err := a.c.RunSkillWatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(skillNotices(a.roomMessages(alerts))); n != 1 {
		t.Fatalf("told twice: %d notices", n)
	}

	// the person imports what is new: the status follows the project
	a.call("skill.importAll", map[string]any{"projectId": p.ID, "registryId": "cloudflare-skills"}, &res)
	if strings.Join(res.Imported, ",") != "queues" || strings.Join(res.Updated, ",") != "wrangler" || strings.Join(res.Unchanged, ",") != "durable-objects" {
		t.Fatalf("%+v", res)
	}
	a.call("skill.registryStatus", map[string]any{}, &sts)
	if ch = statusOf(sts, "cloudflare-skills").Projects[p.ID]; len(ch.Additions)+len(ch.Updates) != 0 {
		t.Fatalf("still listed after import: %+v", ch)
	}
	// nothing was accepted on the way
	a.call("skill.list", map[string]any{"projectId": p.ID, "statuses": []string{skills.StatusAccepted}}, &list)
	if len(list) != 0 {
		t.Fatalf("accepted without a person: %+v", list)
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
