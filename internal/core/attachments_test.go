package core_test

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/room"
)

// A human attaches files to a room message: the files stay on disk, the message lists them, an agent sees a short text file
// (secret-scanned) as untrusted data and only the name of anything else, and the human can save each file back unchanged.
func TestRoomAttachments(t *testing.T) {
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
	secret := "ghp_" + strings.Repeat("aB3dE5fG7h", 3) + "aB3dE5"
	text := "notes for the team\napi key: " + secret + "\n"
	bin := []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 3}
	b64 := base64.StdEncoding.EncodeToString
	var posted room.Posted
	a.call("room.attach", map[string]any{"roomId": ch.ID, "body": "see attached", "files": []map[string]string{
		{"name": "../../etc/notes.txt", "data": b64([]byte(text))},
		{"name": "logo.png", "data": b64(bin)},
	}}, &posted)
	atts := posted.Message.Attachments()
	if len(atts) != 2 || atts[0].Name != "notes.txt" || atts[1].Name != "logo.png" || posted.Message.Body != "see attached" {
		t.Fatalf("attachments %+v body %q", atts, posted.Message.Body)
	}
	if atts[0].Text == "" || strings.Contains(atts[0].Text, secret) || !strings.Contains(atts[0].Text, "notes for the team") {
		t.Fatalf("the agent-visible text must be scanned: %q", atts[0].Text)
	}
	if atts[1].Text != "" || atts[1].Mime != "image/png" {
		t.Fatalf("binary attachment %+v", atts[1])
	}
	rendered := room.RenderContext(room.ContextSelection{Messages: []room.Message{posted.Message}})
	if !strings.Contains(rendered, `<untrusted source="attachment:notes.txt"`) || !strings.Contains(rendered, `attached file "logo.png"`) || strings.Contains(rendered, secret) {
		t.Fatalf("rendered context:\n%s", rendered)
	}

	// the human saves the original back, unchanged (the secret is the human's own file)
	var got core.AttachmentData
	a.call("room.attachment", map[string]string{"id": atts[0].ID}, &got)
	if string(got.Data) != text || got.Name != "notes.txt" {
		t.Fatalf("saved %q as %q", got.Data, got.Name)
	}
	// a file changed on disk after it was attached is refused
	var path string
	if err := a.c.DB.Reader().QueryRow(`SELECT path FROM room_attachments WHERE id=?`, atts[1].ID).Scan(&path); err != nil || !strings.Contains(path, ch.ID) {
		t.Fatalf("path %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("swapped"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.c.Call(t.Context(), "room.attachment", mustJSON(map[string]string{"id": atts[1].ID})); err == nil {
		t.Fatal("a changed file was served")
	}

	// limits
	five := []map[string]string{}
	for range 5 {
		five = append(five, map[string]string{"name": "a.txt", "data": b64([]byte("x"))})
	}
	for _, bad := range []map[string]any{
		{"roomId": ch.ID, "files": five},
		{"roomId": ch.ID, "files": []map[string]string{}},
		{"roomId": ch.ID, "files": []map[string]string{{"name": "x", "data": "%%%"}}},
		{"roomId": "room_nope", "files": []map[string]string{{"name": "x", "data": b64([]byte("x"))}}},
	} {
		if _, err := a.c.Call(t.Context(), "room.attach", mustJSON(bad)); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
}
