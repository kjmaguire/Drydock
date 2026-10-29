package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/safe"
)

// Fetching documentation out of a repository for an expert pack (a git_docs source, decision P110). It never checks anything
// out and never runs anything the repository carries: a fresh bare repository is made in a temporary folder, the ref is
// fetched shallow (depth 1) with no blobs at all, the tree under the docs path is listed, and only the blobs of the files the
// caller wants are fetched, by id, and read with cat-file (which runs no filter, no hook, no LFS). Submodules are never
// fetched (a gitlink is not followed), symbolic links are not followed, and every program a configuration could start is
// switched off: hooks, fsmonitor, credential helpers, askpass. The person's own git configuration is not read (a url
// insteadOf there could send the fetch elsewhere), and no proxy is used, as with the pack crawler: the address is checked by
// the caller and pinned (Resolve), so what git connects to is the address that was checked. The folder is watched while git
// works, and a fetch that grows it past MaxRepoBytes is stopped. The folder is removed afterwards.

// DocsFetch is one fetch of a repository's documentation.
type DocsFetch struct {
	URL  string // https (http only with AllowHTTP)
	Ref  string // a branch, a tag or a commit
	Path string // the folder inside the repository, "" for all of it
	// Exts are the file extensions to read (".md"); the others are counted and skipped.
	Exts []string
	// Resolve pins the address git connects to: "host:port:address" (curl's CURLOPT_RESOLVE form). Empty: git resolves.
	Resolve string
	// TempDir is where the temporary bare repository is made.
	TempDir string
	// MaxRepoBytes bounds what the fetch may download (the temporary folder's size); MaxFileBytes one file; MaxFiles the files read.
	MaxRepoBytes int64
	MaxFileBytes int64
	MaxFiles     int
	// Timeout bounds the whole fetch.
	Timeout time.Duration
	// AllowHTTP allows a plain http URL (tests against a local server only).
	AllowHTTP bool
}

// DocsFile is one file read from the repository.
type DocsFile struct {
	Path string // relative to the repository root, with /
	Data []byte
}

// DocsResult is what a fetch read.
type DocsResult struct {
	Commit  string
	Files   []DocsFile
	Skipped map[string]int // why files under the path were not read: "not a text document", "too large", "symbolic link", "submodule", "more files than are read"
}

// Errors of a docs fetch.
var (
	ErrDocsTooLarge = errors.New("the repository is larger than a docs fetch may download")
	ErrDocsRef      = errors.New("the ref is not a plain branch, tag or commit name")
	ErrDocsPath     = errors.New("the docs path is not a plain folder inside the repository")
)

var docsRefRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)

// ValidDocsRef reports whether ref is a name a docs fetch passes to git: no option, no revision expression, no "..".
func ValidDocsRef(ref string) bool {
	return docsRefRe.MatchString(ref) && !strings.Contains(ref, "..") && !strings.HasSuffix(ref, ".lock") && !strings.HasSuffix(ref, "/") &&
		!strings.Contains(ref, "//") && !strings.Contains(ref, "@{")
}

// CleanDocsPath returns the docs path in git's form ("docs/guide"), or an error for a path that leaves the repository or is
// not plain.
func CleanDocsPath(p string) (string, error) {
	p = strings.TrimSpace(strings.ReplaceAll(p, `\`, "/"))
	if p == "" || p == "." || p == "/" {
		return "", nil
	}
	if strings.HasPrefix(p, "/") || strings.ContainsAny(p, ":*?[]\x00") || strings.HasPrefix(p, "-") {
		return "", ErrDocsPath
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", ErrDocsPath
	}
	for _, seg := range strings.Split(c, "/") {
		if strings.EqualFold(seg, ".git") {
			return "", ErrDocsPath
		}
	}
	return c, nil
}

// FetchDocs fetches the files under req.Path at req.Ref. See the comment above for what it never does.
func FetchDocs(ctx context.Context, req DocsFetch) (DocsResult, error) {
	res := DocsResult{Skipped: map[string]int{}}
	if !ValidDocsRef(req.Ref) {
		return res, fmt.Errorf("%w: %q", ErrDocsRef, req.Ref)
	}
	sub, err := CleanDocsPath(req.Path)
	if err != nil {
		return res, fmt.Errorf("%w: %q", err, req.Path)
	}
	switch {
	case strings.HasPrefix(req.URL, "https://"):
	case req.AllowHTTP && strings.HasPrefix(req.URL, "http://"):
	default:
		return res, errors.New("git_docs: the repository URL must be https")
	}
	if req.Timeout <= 0 {
		req.Timeout = 10 * time.Minute
	}
	if req.MaxFiles <= 0 {
		req.MaxFiles = 500
	}
	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()
	if err := os.MkdirAll(req.TempDir, 0o755); err != nil {
		return res, err
	}
	dir, err := os.MkdirTemp(req.TempDir, "git-docs-*")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(dir)

	d := docsGit{dir: dir, req: req}
	if err := os.WriteFile(filepath.Join(dir, emptyConfig), nil, 0o644); err != nil {
		return res, err
	}
	if _, err := d.run(ctx, nil, "init", "-q", "--bare", "."); err != nil {
		return res, err
	}
	// the watchdog: the folder may not outgrow the bound while git downloads into it
	stop := make(chan struct{})
	var over error
	watch := make(chan struct{})
	safe.Go("git.FetchDocs", func() {
		defer close(watch)
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if req.MaxRepoBytes > 0 && dirSize(dir) > req.MaxRepoBytes {
					over = fmt.Errorf("%w (more than %d MB)", ErrDocsTooLarge, req.MaxRepoBytes>>20)
					cancel()
					return
				}
			}
		}
	})
	defer func() {
		close(stop)
		<-watch
	}()
	tooLarge := func(err error) error {
		select {
		case <-watch:
			if over != nil {
				return over
			}
		default:
		}
		return err
	}
	// a fetch can finish between two looks of the watchdog: the size is checked again after each one
	grown := func() error {
		if req.MaxRepoBytes > 0 && dirSize(dir) > req.MaxRepoBytes {
			return fmt.Errorf("%w (more than %d MB)", ErrDocsTooLarge, req.MaxRepoBytes>>20)
		}
		return nil
	}

	if _, err := d.run(ctx, nil, "fetch", "--quiet", "--depth=1", "--no-tags", "--recurse-submodules=no", "--filter=blob:none", "--", req.URL, req.Ref); err != nil {
		return res, tooLarge(err)
	}
	if err := grown(); err != nil {
		return res, err
	}
	out, err := d.run(ctx, nil, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
	if err != nil {
		return res, tooLarge(err)
	}
	res.Commit = strings.TrimSpace(out)

	args := []string{"ls-tree", "-r", "-z", "--full-tree", res.Commit}
	if sub != "" {
		args = append(args, "--", sub)
	}
	out, err = d.run(ctx, nil, args...)
	if err != nil {
		return res, tooLarge(err)
	}
	var want []docsEntry
	exts := map[string]bool{}
	for _, e := range req.Exts {
		exts[strings.ToLower(e)] = true
	}
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		meta, p, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 {
			continue
		}
		mode, typ, oid := f[0], f[1], f[2]
		switch {
		case typ == "commit":
			res.Skipped["submodule"]++
		case mode == "120000":
			res.Skipped["symbolic link"]++
		case typ != "blob":
		case !exts[strings.ToLower(path.Ext(p))]:
			res.Skipped["not a text document"]++
		case len(want) >= req.MaxFiles:
			res.Skipped["more files than are read"]++
		default:
			want = append(want, docsEntry{oid: oid, path: p})
		}
	}
	if len(want) == 0 {
		return res, nil
	}
	var ids strings.Builder
	for _, e := range want {
		ids.WriteString(e.oid + "\n")
	}
	if _, err := d.run(ctx, strings.NewReader(ids.String()), "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--recurse-submodules=no",
		"--filter=blob:none", "--stdin", "--", req.URL); err != nil {
		return res, tooLarge(err)
	}
	if err := grown(); err != nil {
		return res, err
	}
	blobs, err := d.catBlobs(ctx, want, req.MaxFileBytes)
	if err != nil {
		return res, tooLarge(err)
	}
	for _, e := range want {
		b, ok := blobs[e.oid]
		if !ok {
			res.Skipped["too large"]++
			continue
		}
		res.Files = append(res.Files, DocsFile{Path: e.path, Data: b})
	}
	return res, nil
}

type docsEntry struct{ oid, path string }

// emptyConfig is the file that stands in for the person's global git configuration in a docs fetch.
const emptyConfig = "drydock-empty.gitconfig"

type docsGit struct {
	dir string
	req DocsFetch
}

// run runs git in the temporary repository with the docs fetch's configuration and returns what it printed (bounded).
func (d docsGit) run(ctx context.Context, stdin io.Reader, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append(d.config(), args...)...)
	cmd.Dir = d.dir
	cmd.Env = d.env()
	cmd.Stdin = stdin
	proc.Harden(cmd)
	stdout := &proc.CapWriter{Max: 64 << 20}
	stderr := &proc.CapWriter{Max: 64 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("git %s: %w", args[0], ctx.Err())
		}
		return "", &gitError{args: args, stderr: strings.TrimSpace(stderr.String()), err: err}
	}
	if stdout.Exceeded() {
		return "", &gitError{args: args, err: ErrOutputTooLarge}
	}
	return stdout.String(), nil
}

// config is the -c settings of every docs call: the hardening of every Drydock git call, and for the network only https (or
// http for a test server), no redirects, verified TLS, no proxy, no credential helper, no askpass.
func (d docsGit) config() []string {
	c := hardening(nil)
	c = append(c, "-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "http.followRedirects=false",
		"-c", "http.sslVerify=true", "-c", "http.proxy=", "-c", "credential.helper=", "-c", "core.askPass=", "-c", "fetch.recurseSubmodules=false",
		"-c", "transfer.fsckObjects=true", "-c", "fetch.writeCommitGraph=false", "-c", "gc.auto=0", "-c", "maintenance.auto=false")
	if d.req.AllowHTTP {
		c = append(c, "-c", "protocol.http.allow=always")
	}
	if d.req.Resolve != "" {
		c = append(c, "-c", "http.curloptResolve="+d.req.Resolve)
	}
	return c
}

// env is the scrubbed environment: no tokens of the app, no GIT_* variable but the certificate ones, no proxy, the person's
// global configuration replaced by none (the system one, which the administrator owns and which tells Git for Windows where its
// certificates are, is kept), never a prompt, and never a lazy fetch of a missing object (a cat-file of a blob that was not
// fetched fails instead of reaching the network again).
func (d docsGit) env() []string {
	names := append(append([]string(nil), proc.BaseEnvNames...), localEnvNames...)
	names = append(names, "GIT_SSL_CAINFO", "GIT_SSL_CAPATH", "GIT_EXEC_PATH")
	var env []string
	for _, kv := range proc.ScrubEnv(os.Environ(), names, nil) {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C", "GIT_PAGER=cat", "GIT_CONFIG_GLOBAL="+filepath.Join(d.dir, emptyConfig),
		"GIT_NO_LAZY_FETCH=1", "GIT_ASKPASS=", "SSH_ASKPASS=", "GIT_PROTOCOL_FROM_USER=0", "GIT_LITERAL_PATHSPECS=1")
	return proc.WithSafeExeSearch(env)
}

// catBlobs reads the wanted blobs with one cat-file --batch. A blob larger than maxFile is left out (its bytes are read and
// dropped), and a blob the fetch did not bring is left out too.
func (d docsGit) catBlobs(ctx context.Context, want []docsEntry, maxFile int64) (map[string][]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append(d.config(), "cat-file", "--batch")...)
	cmd.Dir = d.dir
	cmd.Env = d.env()
	proc.Harden(cmd)
	var in strings.Builder
	for _, e := range want {
		in.WriteString(e.oid + "\n")
	}
	cmd.Stdin = strings.NewReader(in.String())
	stderr := &proc.CapWriter{Max: 64 << 10}
	cmd.Stderr = stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	r := bufio.NewReader(pipe)
	var perr error
	for range want {
		head, err := r.ReadString('\n')
		if err != nil {
			perr = fmt.Errorf("git cat-file: %w", err)
			break
		}
		f := strings.Fields(head)
		if len(f) == 2 && f[1] == "missing" {
			continue
		}
		if len(f) != 3 {
			perr = fmt.Errorf("git cat-file: unexpected %q", strings.TrimSpace(head))
			break
		}
		n, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil || n < 0 {
			perr = fmt.Errorf("git cat-file: unexpected size %q", f[2])
			break
		}
		if maxFile > 0 && n > maxFile || f[1] != "blob" {
			if _, err := io.CopyN(io.Discard, r, n+1); err != nil {
				perr = err
				break
			}
			continue
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(r, b); err != nil {
			perr = err
			break
		}
		if nl, err := r.ReadByte(); err != nil || nl != '\n' {
			perr = errors.New("git cat-file: a blob does not end where its size says")
			break
		}
		out[f[0]] = bytes.Clone(b)
	}
	if perr != nil {
		_ = cmd.Process.Kill()
	}
	_, _ = io.Copy(io.Discard, r)
	werr := cmd.Wait()
	if perr != nil {
		return nil, perr
	}
	if werr != nil {
		return nil, &gitError{args: []string{"cat-file", "--batch"}, stderr: strings.TrimSpace(stderr.String()), err: werr}
	}
	return out, nil
}

// dirSize is the size of the files under dir (what a fetch has downloaded so far).
func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}
