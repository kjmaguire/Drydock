package localllm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DefaultHub is the Hugging Face Hub.
const DefaultHub = "https://huggingface.co"

// Hub reads model repositories on the Hugging Face Hub and downloads their GGUF files. Only public repositories: Drydock
// holds no Hugging Face token.
type Hub struct {
	BaseURL string // DefaultHub; a test's fixture server
	HTTP    *http.Client
}

// Repo is a model repository on the Hub.
type Repo struct {
	ID        string    `json:"id"` // "ggml-org/gpt-oss-20b-GGUF"
	Downloads int64     `json:"downloads"`
	Likes     int64     `json:"likes"`
	Updated   time.Time `json:"updated"`
	Tags      []string  `json:"tags,omitempty"`
}

// Variant is one quantization of a repository's model: one GGUF file, or the shards of one split file.
type Variant struct {
	Name  string   `json:"name"` // "Q4_K_M", or the file's stem when it names no quantization
	Files []File   `json:"files"`
	Size  uint64   `json:"sizeBytes"`
	Quant string   `json:"quant,omitempty"`
	Fit   *Fit     `json:"fit,omitempty"`
	Notes []string `json:"notes,omitempty"`
}

// File is one GGUF file of a repository.
type File struct {
	Path   string `json:"path"`
	Size   uint64 `json:"sizeBytes"`
	SHA256 string `json:"sha256"`
}

func (h *Hub) base() string {
	if h.BaseURL != "" {
		return strings.TrimRight(h.BaseURL, "/")
	}
	return DefaultHub
}

func (h *Hub) client() *http.Client {
	if h.HTTP != nil {
		return h.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

// Trusted reports whether an address is the Hub Drydock talks to; nothing else is fetched.
func (h *Hub) Trusted(u string) bool {
	return strings.HasPrefix(u, h.base()+"/")
}

func (h *Hub) getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := h.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Hugging Face answered HTTP %d", resp.StatusCode)
	}
	return json.Unmarshal(b, out)
}

type rawRepo struct {
	ID           string    `json:"id"`
	ModelID      string    `json:"modelId"`
	Downloads    int64     `json:"downloads"`
	Likes        int64     `json:"likes"`
	LastModified time.Time `json:"lastModified"`
	Tags         []string  `json:"tags"`
	Gated        any       `json:"gated"`
	Private      bool      `json:"private"`
}

func (r rawRepo) repo() (Repo, bool) {
	id := r.ID
	if id == "" {
		id = r.ModelID
	}
	if id == "" || r.Private {
		return Repo{}, false
	}
	if g, ok := r.Gated.(bool); ok && g {
		return Repo{}, false // needs a sign-in Drydock does not have
	}
	if s, ok := r.Gated.(string); ok && s != "" && s != "false" {
		return Repo{}, false
	}
	tags := []string{}
	for _, t := range r.Tags {
		if t == "gguf" || strings.HasPrefix(t, "base_model:") || strings.HasPrefix(t, "license:") || t == "conversational" || t == "text-generation" || t == "endpoints_compatible" || strings.HasPrefix(t, "region:") || strings.HasPrefix(t, "arxiv:") {
			continue
		}
		tags = append(tags, t)
	}
	return Repo{ID: id, Downloads: r.Downloads, Likes: r.Likes, Updated: r.LastModified, Tags: tags}, true
}

// Suggested lists ggml-org's own GGUF repositories, most downloaded first: the models llama.cpp's authors keep current.
func (h *Hub) Suggested(ctx context.Context) ([]Repo, error) {
	return h.list(ctx, url.Values{"author": {"ggml-org"}, "filter": {"gguf"}, "sort": {"downloads"}, "direction": {"-1"}, "limit": {"40"}})
}

// Search finds GGUF repositories by name, most downloaded first.
func (h *Hub) Search(ctx context.Context, query string) ([]Repo, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []Repo{}, nil
	}
	return h.list(ctx, url.Values{"search": {query}, "filter": {"gguf"}, "sort": {"downloads"}, "direction": {"-1"}, "limit": {"30"}})
}

func (h *Hub) list(ctx context.Context, q url.Values) ([]Repo, error) {
	var raw []rawRepo
	if err := h.getJSON(ctx, h.base()+"/api/models?"+q.Encode(), &raw); err != nil {
		return nil, err
	}
	out := []Repo{}
	for _, r := range raw {
		if repo, ok := r.repo(); ok {
			out = append(out, repo)
		}
	}
	return out, nil
}

var repoID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,95}/[A-Za-z0-9][A-Za-z0-9._-]{0,95}$`)

// ValidRepo reports whether s is "owner/name".
func ValidRepo(s string) bool { return repoID.MatchString(s) }

type rawTreeEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size uint64 `json:"size"`
	LFS  *struct {
		OID  string `json:"oid"`
		Size uint64 `json:"size"`
	} `json:"lfs"`
}

// shard is "model-00001-of-00003.gguf".
var shard = regexp.MustCompile(`^(.*)-(\d{5})-of-(\d{5})\.gguf$`)

// quantName finds the quantization in a file name ("Qwen3-8B-Q4_K_M.gguf" is Q4_K_M).
var quantName = regexp.MustCompile(`(?i)(?:^|[-_.])((?:IQ|Q|TQ|MXFP|NVFP|BF|F)\d{1,2}(?:_[A-Z0-9]{1,3}){0,3})(?:[-_.]|$)`)

// Variants lists the model's GGUF files, grouped by quantization, largest last. Projector files (mmproj, for vision) are
// left out: Drydock serves text models. A file the Hub has no SHA-256 for is not offered.
func (h *Hub) Variants(ctx context.Context, repo string) ([]Variant, error) {
	if !ValidRepo(repo) {
		return nil, fmt.Errorf("%q is not a repository (owner/name)", repo)
	}
	var tree []rawTreeEntry
	if err := h.getJSON(ctx, h.base()+"/api/models/"+repo+"/tree/main?recursive=true", &tree); err != nil {
		return nil, err
	}
	byName := map[string]*Variant{}
	var order []string
	for _, e := range tree {
		if e.Type != "file" || !strings.HasSuffix(strings.ToLower(e.Path), ".gguf") || strings.Contains(strings.ToLower(path.Base(e.Path)), "mmproj") {
			continue
		}
		if e.LFS == nil || len(e.LFS.OID) != 64 {
			continue
		}
		base := path.Base(e.Path)
		stem := strings.TrimSuffix(base, ".gguf")
		if m := shard.FindStringSubmatch(base); m != nil {
			stem = m[1]
		}
		name := stem
		quant := ""
		if m := quantName.FindStringSubmatch(stem); m != nil {
			quant = strings.ToUpper(m[1])
			name = quant
		}
		key := path.Dir(e.Path) + "/" + stem
		v, ok := byName[key]
		if !ok {
			v = &Variant{Name: name, Quant: quant}
			byName[key] = v
			order = append(order, key)
		}
		size := e.LFS.Size
		if size == 0 {
			size = e.Size
		}
		v.Files = append(v.Files, File{Path: e.Path, Size: size, SHA256: strings.ToLower(e.LFS.OID)})
		v.Size += size
	}
	out := make([]Variant, 0, len(order))
	for _, k := range order {
		v := byName[k]
		sort.Slice(v.Files, func(i, j int) bool { return v.Files[i].Path < v.Files[j].Path })
		if m := shard.FindStringSubmatch(path.Base(v.Files[0].Path)); m != nil {
			n, _ := strconv.Atoi(m[3])
			if n != len(v.Files) {
				v.Notes = append(v.Notes, fmt.Sprintf("split into %d files, %d listed", n, len(v.Files)))
			}
		}
		out = append(out, *v)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Size < out[j].Size })
	return out, nil
}

// DownloadProgress is told how far a model download is.
type DownloadProgress struct {
	File  string `json:"file"`
	Done  uint64 `json:"done"`  // bytes of the whole variant so far
	Total uint64 `json:"total"` // bytes of the whole variant
}

// MaxModelFile is the largest single GGUF file Drydock downloads.
const MaxModelFile = 200 << 30

// Download fetches the variant's files into dir (flat, by base name), resuming a file that was partly downloaded, and
// checks each against the SHA-256 the Hub records. A file that does not match is deleted and the download fails: no model
// is served that is not what the Hub says it is. ctx cancels it; the partial file stays for a later resume.
func (h *Hub) Download(ctx context.Context, repo string, v Variant, dir string, report func(DownloadProgress)) error {
	if !ValidRepo(repo) {
		return fmt.Errorf("%q is not a repository (owner/name)", repo)
	}
	if report == nil {
		report = func(DownloadProgress) {}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var done uint64
	for _, f := range v.Files {
		base := path.Base(f.Path)
		if base == "" || base == "." || strings.ContainsAny(base, `/\`) {
			return fmt.Errorf("bad file name %q", f.Path)
		}
		if f.Size > MaxModelFile {
			return fmt.Errorf("%s is larger than %d GB", base, MaxModelFile>>30)
		}
		dst := filepath.Join(dir, base)
		if sum, err := fileSHA256(dst); err == nil && sum == f.SHA256 {
			done += f.Size
			report(DownloadProgress{File: base, Done: done, Total: v.Size})
			continue
		}
		start := done
		err := h.downloadFile(ctx, h.base()+"/"+repo+"/resolve/main/"+escapePath(f.Path), f, dst, func(n uint64) {
			report(DownloadProgress{File: base, Done: start + n, Total: v.Size})
		})
		if err != nil {
			return err
		}
		done += f.Size
	}
	report(DownloadProgress{Done: v.Size, Total: v.Size})
	return nil
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

func (h *Hub) downloadFile(ctx context.Context, u string, f File, dst string, progress func(n uint64)) error {
	if !h.Trusted(u) {
		return fmt.Errorf("refusing to download from %s", u)
	}
	part := dst + ".part"
	var have int64
	if st, err := os.Stat(part); err == nil {
		have = st.Size()
		if uint64(have) > f.Size {
			have = 0
			_ = os.Remove(part)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	client := &http.Client{Transport: h.client().Transport, Timeout: 0} // a model file takes as long as it takes
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not download %s: %w", path.Base(f.Path), err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusPartialContent && have > 0:
	case resp.StatusCode == http.StatusOK:
		have = 0 // the server ignored the range: start over
	default:
		return fmt.Errorf("could not download %s: HTTP %d", path.Base(f.Path), resp.StatusCode)
	}
	flags := os.O_CREATE | os.O_WRONLY
	if have > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	out, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	pw := &countWriter{w: out, done: uint64(have), report: progress}
	_, err = io.Copy(pw, io.LimitReader(resp.Body, int64(f.Size)-have+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("could not download %s: %w", path.Base(f.Path), err)
	}
	if pw.done != f.Size {
		if pw.done > f.Size {
			_ = os.Remove(part)
		}
		return fmt.Errorf("%s: got %d bytes, the Hub says %d", path.Base(f.Path), pw.done, f.Size)
	}
	sum, err := fileSHA256(part)
	if err != nil {
		return err
	}
	if sum != f.SHA256 {
		_ = os.Remove(part)
		return fmt.Errorf("%s does not match the SHA-256 the Hub records (got %s, want %s); it was deleted", path.Base(f.Path), sum, f.SHA256)
	}
	return os.Rename(part, dst)
}

type countWriter struct {
	w      io.Writer
	done   uint64
	last   uint64
	report func(uint64)
}

func (c *countWriter) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	c.done += uint64(n)
	if c.done-c.last >= 4<<20 || err != nil {
		c.last = c.done
		c.report(c.done)
	}
	return n, err
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ErrNotDownloaded is a model that is not on this PC.
var ErrNotDownloaded = errors.New("the model is not downloaded")
