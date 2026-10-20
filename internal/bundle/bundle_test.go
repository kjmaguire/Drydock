package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/runners"
)

const prefix = "https://github.com/opengrep/opengrep/releases/download/"

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestReadWithoutAManifestIsNoBundle(t *testing.T) {
	for _, dir := range []string{"", t.TempDir()} {
		m, err := Read(dir)
		if err != nil || m != nil {
			t.Fatalf("Read(%q) = %v, %v; want nil, nil", dir, m, err)
		}
	}
}

func TestReadRefusesAFormatItDoesNotKnow(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"version":2,"scan":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(dir); err == nil || !strings.Contains(err.Error(), "format 2") {
		t.Fatalf("err = %v, want the format named", err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(dir); err == nil {
		t.Fatal("a broken bundle.json was read")
	}
}

func TestReadReadsTheTools(t *testing.T) {
	dir := t.TempDir()
	doc := `{"version":1,"built":"2026-10-20T00:00:00Z","scan":[{"tool":"opengrep","tag":"v1.31.0","asset":{"name":"opengrep_windows_x86.zip","url":"` + prefix + `v1.31.0/opengrep_windows_x86.zip","sha256":"` + strings.Repeat("a", 64) + `"}}],"llama":{"source":"turboquant","tag":"b1","backend":"vulkan","archive":{"name":"x.zip"}}}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Scan) != 1 || m.Scan[0].Tool != "opengrep" || m.Scan[0].Tag != "v1.31.0" || m.Scan[0].Asset.Name != "opengrep_windows_x86.zip" {
		t.Fatalf("scan = %+v", m.Scan)
	}
	if m.Llama == nil || m.Llama.Source != "turboquant" || m.Llama.Backend != "vulkan" {
		t.Fatalf("llama = %+v", m.Llama)
	}
}

// Stage copies a bundled file into the cache only when it is what bundle.json says: a plain name, from the tool's own
// releases, with the recorded SHA-256.
func TestStageCopiesOnlyAFileThatMatches(t *testing.T) {
	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "cache")
	body := []byte("the release file")
	if err := os.WriteFile(filepath.Join(dir, "opengrep_windows_x86.zip"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	good := runners.Asset{Name: "opengrep_windows_x86.zip", URL: prefix + "v1.31.0/opengrep_windows_x86.zip", SHA256: sum(body)}
	got, err := Stage(dir, good, prefix, cache)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(got); string(b) != string(body) || filepath.Dir(got) != cache {
		t.Fatalf("staged %s = %q", got, b)
	}
	// staging again finds it in place
	if again, err := Stage(dir, good, prefix, cache); err != nil || again != got {
		t.Fatalf("again = %s, %v", again, err)
	}

	bad := []struct {
		name string
		a    runners.Asset
		want string
	}{
		{"a path, not a name", runners.Asset{Name: `..\evil.zip`, URL: good.URL, SHA256: good.SHA256}, "bad file name"},
		{"a parent folder", runners.Asset{Name: "..", URL: good.URL, SHA256: good.SHA256}, "bad file name"},
		{"from elsewhere", runners.Asset{Name: good.Name, URL: "https://example.com/opengrep_windows_x86.zip", SHA256: good.SHA256}, "did not come from"},
		{"no digest", runners.Asset{Name: good.Name, URL: good.URL}, "no SHA-256"},
		{"another digest", runners.Asset{Name: good.Name, URL: good.URL, SHA256: strings.Repeat("0", 64)}, "does not match"},
	}
	for _, c := range bad {
		other := filepath.Join(t.TempDir(), "cache")
		if _, err := Stage(dir, c.a, prefix, other); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
		if _, err := os.Stat(other); err == nil {
			t.Errorf("%s: the cache was made", c.name)
		}
	}
	// a file that is not in the folder is an error, not a download
	if _, err := Stage(t.TempDir(), good, prefix, filepath.Join(t.TempDir(), "c")); err == nil {
		t.Fatal("a missing file was staged")
	}
}
