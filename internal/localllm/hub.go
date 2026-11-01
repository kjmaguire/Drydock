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

// Hub reads model repositories on the Hugging Face Hub and downloads their GGUF files: public repositories, and gated ones
// once the person gave Drydock a Hugging Face token and accepted the model's licence on the Hub (decision P111).
type Hub struct {
	BaseURL string // DefaultHub; a test's fixture server
	HTTP    *http.Client
	// Token is the person's Hugging Face token ("" when there is none). It is sent as the Authorization header of a file
	// download from the Hub's own address and nowhere else: not with a listing, and not to the host a download is redirected
	// to (the Hub sends the bytes from a CDN at a signed address that needs no token).
	Token func() string
}

// Repo is a model repository on the Hub.
type Repo struct {
	ID        string    `json:"id"` // "ggml-org/gpt-oss-20b-GGUF"
	Downloads int64     `json:"downloads"`
	Likes     int64     `json:"likes"`
	Updated   time.Time `json:"updated"`
	Tags      []string  `json:"tags,omitempty"`
	// Gated is how the Hub gates the files ("auto" or "manual": a person accepts the licence, which the authors may have to
	// approve), "" for an open repository.
	Gated string `json:"gated,omitempty"`
}

// Variant is one quantization of a repository's model: one GGUF file, or the shards of one split file.
type Variant struct {
	Name  string   `json:"name"` // "Q4_K_M", or the file's stem when it names no quantization
	Files []File   `json:"files"`
	Size  uint64   `json:"sizeBytes"` // the files and the projector
	Quant string   `json:"quant,omitempty"`
	Fit   *Fit     `json:"fit,omitempty"`
	Notes []string `json:"notes,omitempty"`
	// Projector is the repository's multimodal projector (mmproj), downloaded with the model so it can read images (decision
	// P111); nil for a text model.
	Projector *File `json:"projector,omitempty"`
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
	gated := gatedOf(r.Gated)
	tags := []string{}
	for _, t := range r.Tags {
		if t == "gguf" || strings.HasPrefix(t, "base_model:") || strings.HasPrefix(t, "license:") || t == "conversational" || t == "text-generation" || t == "endpoints_compatible" || strings.HasPrefix(t, "region:") || strings.HasPrefix(t, "arxiv:") {
			continue
		}
		tags = append(tags, t)
	}
	return Repo{ID: id, Downloads: r.Downloads, Likes: r.Likes, Updated: r.LastModified, Tags: tags, Gated: gated}, true
}

// gatedOf reads the Hub's gated field: false, or how the files are gated ("auto", "manual"; true is read as "auto").
func gatedOf(v any) string {
	switch g := v.(type) {
	case bool:
		if g {
			return "auto"
		}
	case string:
		if g != "" && g != "false" {
			return g
		}
	}
	return ""
}

// LicenceURL is the repository's page on the Hub, where a person accepts a gated model's licence.
func LicenceURL(repo string) string { return DefaultHub + "/" + repo }

// Info reads one repository's listing: whether it is gated (a private one is not found).
func (h *Hub) Info(ctx context.Context, repo string) (Repo, error) {
	if !ValidRepo(repo) {
		return Repo{}, fmt.Errorf("%q is not a repository (owner/name)", repo)
	}
	var raw rawRepo
	if err := h.getJSON(ctx, h.base()+"/api/models/"+repo, &raw); err != nil {
		return Repo{}, err
	}
	if raw.ID == "" && raw.ModelID == "" {
		raw.ID = repo
	}
	r, ok := raw.repo()
	if !ok {
		return Repo{}, fmt.Errorf("%s is not a public repository", repo)
	}
	return r, nil
}

// GatedError is a download the Hub refused because the repository is gated: without a token, with a token it does not accept,
// or for an account that has not been given access (the licence not accepted yet, or not approved by the authors).
type GatedError struct {
	Repo     string
	Status   int  // the Hub's answer (401 or 403); 0 when Drydock refused before asking (no token)
	HasToken bool // a token was sent
}

func (e *GatedError) Error() string {
	page := LicenceURL(e.Repo)
	switch {
	case !e.HasToken:
		return fmt.Sprintf("%s is a gated model: Hugging Face shares its files only with an account that accepted its licence. Accept it at %s, then add a Hugging Face token on this page.", e.Repo, page)
	case e.Status == http.StatusUnauthorized:
		return "Hugging Face did not accept the token (HTTP 401): add a new one on this page."
	default:
		return fmt.Sprintf("Your Hugging Face account has no access to %s yet: accept its licence at %s (its authors may also have to approve the request).", e.Repo, page)
	}
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

// Variants lists the model's GGUF files, grouped by quantization, largest last. A projector file (mmproj, for vision) is no
// variant of its own: it goes with each variant, which then reads images too (decision P111). A file the Hub has no SHA-256
// for is not offered.
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
	var projectors []File
	for _, e := range tree {
		if e.Type != "file" || !strings.HasSuffix(strings.ToLower(e.Path), ".gguf") {
			continue
		}
		if e.LFS == nil || len(e.LFS.OID) != 64 {
			continue
		}
		if strings.Contains(strings.ToLower(path.Base(e.Path)), "mmproj") {
			if strings.Trim(strings.TrimSuffix(path.Base(e.Path), ".gguf"), ".") != "" {
				size := e.LFS.Size
				if size == 0 {
					size = e.Size
				}
				projectors = append(projectors, File{Path: e.Path, Size: size, SHA256: strings.ToLower(e.LFS.OID)})
			}
			continue
		}
		base := path.Base(e.Path)
		stem := strings.TrimSuffix(base, ".gguf")
		if m := shard.FindStringSubmatch(base); m != nil {
			stem = m[1]
		}
		if strings.Trim(stem, ".") == "" {
			continue // "...gguf" has no name to keep a model under, and a folder named ".." leads out of the store
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
		if p := projectorFor(projectors, path.Dir(v.Files[0].Path)); p != nil {
			v.Projector = p
			v.Size += p.Size
		}
		out = append(out, *v)
	}
	uniqueNames(out)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Size < out[j].Size })
	return out, nil
}

// projectorFor picks the projector a variant in dir loads: one in the same folder before one elsewhere, and of those the 16-bit
// one (F16, then BF16, the precision llama.cpp's own conversions use), then Q8_0, then the first by name.
func projectorFor(ps []File, dir string) *File {
	rank := func(p File) int {
		n := strings.ToUpper(path.Base(p.Path))
		r := 3
		switch {
		case strings.Contains(n, "BF16"):
			r = 1
		case strings.Contains(n, "F16"):
			r = 0
		case strings.Contains(n, "Q8_0"):
			r = 2
		}
		if path.Dir(p.Path) != dir {
			r += 10
		}
		return r
	}
	var best *File
	for i := range ps {
		if best == nil || rank(ps[i]) < rank(*best) || (rank(ps[i]) == rank(*best) && ps[i].Path < best.Path) {
			best = &ps[i]
		}
	}
	if best == nil {
		return nil
	}
	p := *best
	return &p
}

// uniqueNames keeps every variant's name distinct, since the name is the model id's second half and the folder a download
// goes to: two files of the same quantization in different folders (a repository keeping its split quants in folders of
// their own) are named by their file stem, and when those agree too by their folder and stem ("Q8_0-Model-Q8_0").
func uniqueNames(vs []Variant) {
	count := func() map[string]int {
		c := map[string]int{}
		for _, v := range vs {
			c[v.Name]++
		}
		return c
	}
	stem := func(v Variant) string {
		base := path.Base(v.Files[0].Path)
		if m := shard.FindStringSubmatch(base); m != nil {
			return m[1]
		}
		return strings.TrimSuffix(base, ".gguf")
	}
	c := count()
	for i := range vs {
		if c[vs[i].Name] > 1 {
			vs[i].Name = stem(vs[i])
		}
	}
	c = count()
	for i := range vs {
		if dir := path.Dir(vs[i].Files[0].Path); c[vs[i].Name] > 1 && dir != "." {
			vs[i].Name = strings.ReplaceAll(dir, "/", "-") + "-" + stem(vs[i])
		}
	}
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
	files := v.Files
	if v.Projector != nil {
		files = append(append([]File{}, v.Files...), *v.Projector)
	}
	for _, f := range files {
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
		err := h.downloadFile(ctx, repo, h.base()+"/"+repo+"/resolve/main/"+escapePath(f.Path), f, dst, func(n uint64) {
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

func (h *Hub) downloadFile(ctx context.Context, repo, u string, f File, dst string, progress func(n uint64)) error {
	if !h.Trusted(u) {
		return fmt.Errorf("refusing to download from %s", u)
	}
	part := dst + ".part"
	var have int64
	if st, err := os.Stat(part); err == nil {
		have = st.Size()
		switch {
		case uint64(have) > f.Size:
			have = 0
			_ = os.Remove(part)
		case uint64(have) == f.Size:
			// every byte arrived before Drydock last closed, the check or the rename did not: finish those here, since
			// asking the Hub for the bytes from the end is answered 416 forever
			if sum, err := fileSHA256(part); err == nil && sum == f.SHA256 {
				progress(f.Size)
				return os.Rename(part, dst)
			}
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
	token := ""
	if h.Token != nil {
		token = strings.TrimSpace(h.Token())
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	hubHost := req.URL.Host
	client := &http.Client{Transport: h.client().Transport, Timeout: 0, // a model file takes as long as it takes
		// the token goes to the Hub's own host only: Go keeps the header on a redirect to a subdomain (cdn-lfs.huggingface.co),
		// so it is taken off any request that is not for the host it was given to
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if r.URL.Host != hubHost || r.URL.Scheme != via[0].URL.Scheme {
				r.Header.Del("Authorization")
			}
			return nil
		}}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not download %s: %w", path.Base(f.Path), err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusPartialContent && have > 0:
	case resp.StatusCode == http.StatusOK:
		have = 0 // the server ignored the range: start over
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		// the Hub answers 401 to a gated file without a token, and 403 to an account without access to it
		return &GatedError{Repo: repo, Status: resp.StatusCode, HasToken: token != ""}
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
