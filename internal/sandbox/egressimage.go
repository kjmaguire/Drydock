package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
)

// egressImage returns the local image that holds only the drydock-egress binary for the engine's architecture, creating it
// with `docker import` from the binary that ships with Drydock when it is not there yet. No registry is involved.
func (m *Manager) egressImage(ctx context.Context, arch string) (string, error) {
	find := m.EgressBin
	if find == nil {
		find = EgressBinary
	}
	p, err := find(arch)
	if err != nil {
		return "", err
	}
	bin, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("reading the egress proxy %s: %w", p, err)
	}
	sum := sha256.Sum256(bin)
	tag := "drydock-egress:" + hex.EncodeToString(sum[:6])
	if m.Engine.HasImage(ctx, tag) {
		return tag, nil
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "drydock-egress", Mode: 0o755, Size: int64(len(bin)), Typeflag: tar.TypeReg}); err != nil {
		return "", err
	}
	if _, err := tw.Write(bin); err != nil {
		return "", err
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	if _, err := m.Engine.Run(ctx, &buf, "import",
		"--change", `ENTRYPOINT ["/drydock-egress"]`, "--change", "USER 65534:65534", "--change", "EXPOSE 3128",
		"--change", "LABEL "+LabelOwner+"=drydock", "-", tag); err != nil {
		return "", fmt.Errorf("creating the egress proxy image: %w", err)
	}
	return tag, nil
}
