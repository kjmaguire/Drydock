package core_test

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/room"
)

// The human exports a room's Markdown minutes and its ADR candidates; each export is an immutable, verifiable snapshot.
func TestRoomMinutesAndADRExports(t *testing.T) {
	a := open(t, "")
	p, _ := a.setup("gocalc")
	var rooms []room.Room
	a.call("room.list", map[string]string{"projectId": p.ID}, &rooms)
	var ch room.Room
	for _, r := range rooms {
		if r.Kind == "channel" {
			ch = r
		}
	}
	a.call("room.post", map[string]string{"roomId": ch.ID, "body": "Kickoff: we build a calculator."}, nil)
	a.call("room.post", map[string]string{"roomId": ch.ID, "body": "[DECISION] Keep the parser in Go\nIt is simpler to ship."}, nil)

	var minutes room.Export
	a.call("room.export", map[string]any{"roomId": ch.ID, "kind": "minutes", "title": "Kickoff"}, &minutes)
	if minutes.Format != "markdown" || !strings.Contains(minutes.Content, "Kickoff: we build a calculator.") || minutes.SHA256 == "" {
		t.Fatalf("minutes %+v", minutes)
	}
	var adr room.Export
	a.call("room.export", map[string]any{"roomId": ch.ID, "kind": "adr_candidates"}, &adr)
	if !strings.Contains(adr.Content, "Keep the parser in Go") {
		t.Fatalf("adr %+v", adr)
	}
	var cands []room.ADRCandidate
	a.call("room.adrCandidates", map[string]string{"roomId": ch.ID}, &cands)
	if len(cands) != 1 || !strings.Contains(cands[0].Title+cands[0].Decision, "Keep the parser in Go") {
		t.Fatalf("candidates %+v", cands)
	}
	var list []room.Export
	a.call("room.exports", map[string]string{"roomId": ch.ID}, &list)
	if len(list) != 2 {
		t.Fatalf("list %+v", list)
	}
	var v struct {
		OK bool `json:"ok"`
	}
	a.call("room.exportVerify", map[string]string{"id": minutes.ID}, &v)
	if !v.OK {
		t.Fatal("a fresh export does not verify")
	}
	if _, err := a.c.Call(t.Context(), "room.export", mustJSON(map[string]any{"roomId": ch.ID, "kind": "everything"})); err == nil {
		t.Fatal("an unknown export kind was accepted")
	}
}
