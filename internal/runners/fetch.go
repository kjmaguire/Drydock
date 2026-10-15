package runners

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxArchive is the largest runner archive Drydock downloads (the 2.3xx archives are about 100 to 230 MB).
const MaxArchive = 512 << 20

// DownloadOptions widen Download for archives that are not the runner's (the local models' llama.cpp builds, decision P65).
type DownloadOptions struct {
	// Prefix is the only place the archive may be fetched from (default DownloadPrefix).
	Prefix string
	// MaxBytes is the largest archive accepted (default MaxArchive).
	MaxBytes int64
	// Progress, when set, is told how many bytes have arrived so far and the asset's size (0 when the release gave none).
	Progress func(done, total int64)
}

// Download puts the asset's archive in dir (as dir/<asset name>) and checks its SHA-256 against the release notes. An
// archive already there with the right SHA-256 is reused; one with the wrong SHA-256 is replaced. A download that does not
// match is deleted and refused: nothing unchecked is ever run.
func Download(ctx context.Context, client *http.Client, a Asset, dir string) (string, error) {
	return DownloadWith(ctx, client, a, dir, DownloadOptions{})
}

// DownloadWith is Download with the archive's source prefix, size limit and progress chosen by the caller.
func DownloadWith(ctx context.Context, client *http.Client, a Asset, dir string, o DownloadOptions) (string, error) {
	if o.Prefix == "" {
		o.Prefix = DownloadPrefix
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = MaxArchive
	}
	if !strings.HasPrefix(a.URL, o.Prefix) {
		return "", fmt.Errorf("refusing to download %s from %s", a.Name, a.URL)
	}
	if strings.ContainsAny(a.Name, `/\`) || a.Name == "" || a.Name == "." || a.Name == ".." {
		return "", fmt.Errorf("bad archive name %q", a.Name)
	}
	if len(a.SHA256) != 64 {
		return "", fmt.Errorf("the archive %s has no SHA-256 to check", a.Name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, a.Name)
	if sum, err := fileSHA256(dst); err == nil && sum == a.SHA256 {
		if o.Progress != nil {
			o.Progress(a.Size, a.Size)
		}
		return dst, nil
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not download %s: %w", a.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("could not download %s: HTTP %d", a.Name, resp.StatusCode)
	}
	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	var w io.Writer = io.MultiWriter(tmp, h)
	if o.Progress != nil {
		w = &progressWriter{w: w, total: a.Size, report: o.Progress}
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, o.MaxBytes+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("could not download %s: %w", a.Name, err)
	}
	if n > o.MaxBytes {
		return "", fmt.Errorf("the archive %s is larger than %d MB", a.Name, o.MaxBytes>>20)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != a.SHA256 {
		return "", fmt.Errorf("the archive %s does not match the SHA-256 its release publishes (got %s, want %s); it was deleted", a.Name, got, a.SHA256)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return "", err
	}
	return dst, nil
}

// progressWriter reports the bytes written so far, at most about every 256 KB and at the end.
type progressWriter struct {
	w      io.Writer
	total  int64
	done   int64
	last   int64
	report func(done, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.done += int64(n)
	if p.done-p.last >= 256<<10 || err != nil || (p.total > 0 && p.done >= p.total) {
		p.last = p.done
		p.report(p.done, p.total)
	}
	return n, err
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
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

// Limits of an archive's contents.
const (
	maxEntries  = 20000
	maxUnpacked = 2 << 30
)

// Unzip unpacks a zip archive into dir, which must not exist yet. Every entry must stay inside dir: an absolute name, a ".."
// step, a link or a device is refused and nothing is left behind.
func Unzip(archive, dir string) (err error) {
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("%s already exists", dir)
	}
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer func() { _ = zr.Close() }()
	if len(zr.File) > maxEntries {
		return fmt.Errorf("the archive has more than %d entries", maxEntries)
	}
	tmp := dir + ".partial"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tmp)
		}
	}()
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	var total int64
	for _, f := range zr.File {
		name, err := entryPath(f.Name)
		if err != nil {
			return err
		}
		if name == "" {
			continue
		}
		target := filepath.Join(tmp, name)
		mode := f.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		case !mode.IsRegular():
			return fmt.Errorf("the archive entry %s is not a file or folder", f.Name)
		}
		total += int64(f.UncompressedSize64)
		if total > maxUnpacked || f.UncompressedSize64 > maxUnpacked {
			return errors.New("the archive unpacks to more than 2 GB")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := unzipFile(f, target, mode.Perm()|0o600); err != nil {
			return err
		}
	}
	return os.Rename(tmp, dir)
}

func unzipFile(f *zip.File, target string, perm os.FileMode) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(rc, int64(f.UncompressedSize64)+1)); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// entryPath is an archive entry's name as a relative path inside the folder, or an error when it would leave it.
func entryPath(name string) (string, error) {
	n := strings.ReplaceAll(name, `\`, "/")
	if strings.HasPrefix(n, "/") || (len(n) > 1 && n[1] == ':') {
		return "", fmt.Errorf("the archive entry %s is an absolute path", name)
	}
	for _, part := range strings.Split(n, "/") {
		if part == ".." {
			return "", fmt.Errorf("the archive entry %s leaves its folder", name)
		}
	}
	clean := filepath.Clean(filepath.FromSlash(strings.TrimSuffix(n, "/")))
	if clean == "." {
		return "", nil
	}
	return clean, nil
}

// CopyTree copies the folder src to dst (which must not exist): files and folders only.
func CopyTree(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%s already exists", dst)
	}
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()|0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}
