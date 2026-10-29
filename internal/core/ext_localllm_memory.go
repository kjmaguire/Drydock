package core

import (
	"context"
	"os"

	"github.com/kjmaguire/drydock/internal/localllm"
)

// What the Models on this PC page shows about memory and use (decision P97): each downloaded model's KV cache estimate (read
// from its GGUF header) counted in its fit, the served model's memory for the memory bar, and the agents that use the server.

// launchCache is the cache type the next start of the server uses: the setting, or the installed build's first type when
// the build does not take it (as serve does).
func (l *localExt) launchCache(s LocalSettings) string {
	cache := s.CacheType
	if in, ok := l.m.Runtime.Installed(); ok {
		if src, ok := localllm.SourceByID(in.Source); ok && !src.CacheType(cache) && len(src.CacheTypes) > 0 {
			cache = src.CacheTypes[0]
		}
	}
	return cache
}

// ggufOf reads a model's GGUF header once while its first file stays as it is.
func (l *localExt) ggufOf(m localllm.Model) (localllm.GGUFInfo, error) {
	if len(m.Files) == 0 {
		return localllm.GGUFInfo{}, os.ErrNotExist
	}
	p := m.Files[0]
	st, err := os.Stat(p)
	if err != nil {
		return localllm.GGUFInfo{}, err
	}
	l.mu.Lock()
	e, ok := l.gguf[p]
	l.mu.Unlock()
	if ok && e.size == st.Size() && e.mod.Equal(st.ModTime()) {
		return e.info, e.err
	}
	info, err := localllm.ReadGGUF(p)
	// a split model's MTP heads are in a later file than its header (decision P120)
	if t := localllm.MTPTensor(info); err == nil && !info.MTP && t != "" {
		for _, f := range m.Files[1:] {
			if ok, _ := localllm.HasTensor(f, t); ok {
				info.MTP = true
				break
			}
		}
	}
	l.mu.Lock()
	if l.gguf == nil {
		l.gguf = map[string]ggufEntry{}
	}
	l.gguf[p] = ggufEntry{size: st.Size(), mod: st.ModTime(), info: info, err: err}
	l.mu.Unlock()
	return info, err
}

// kvOf is a model's KV cache estimate at a context size (0: the model's own) and cache type; unknown, with the reason, when
// its header cannot be read.
func (l *localExt) kvOf(m localllm.Model, contextSize int, cache string) localllm.KVCache {
	info, err := l.ggufOf(m)
	if err != nil {
		return localllm.KVCache{ContextSize: contextSize, CacheType: cache, Note: "The model's GGUF header could not be read, so its cache is not estimated."}
	}
	return localllm.EstimateKV(info, contextSize, cache)
}

// servedOf is the served model's memory against the GPU with the most free memory, else the RAM.
func servedOf(m localllm.Model, kv localllm.KVCache, f localllm.Fit, d localllm.Devices, mem localllm.Memory) *LocalServed {
	out := &LocalServed{ModelID: m.ID, ModelBytes: m.Size, KV: kv, Fit: f, Device: "ram", DeviceTotal: mem.Total, DeviceFree: mem.Available}
	if kv.Known {
		out.KVBytes = kv.Bytes
	}
	var gpu *localllm.Device
	for i := range d.Devices {
		if d.Devices[i].Kind != "CPU" && (gpu == nil || d.Devices[i].Free > gpu.Free) {
			gpu = &d.Devices[i]
		}
	}
	if gpu != nil {
		out.Device, out.DeviceName, out.DeviceTotal, out.DeviceFree = "gpu", gpu.Name, gpu.Total, gpu.Free
	}
	return out
}

// usedBy lists the agents of every project whose provider is the server's provider row, by project and name.
func (l *localExt) usedBy(ctx context.Context, providerID string) []LocalUser {
	out := []LocalUser{}
	if providerID == "" {
		return out
	}
	rows, err := l.c.DB.Reader().QueryContext(ctx, `SELECT a.id, a.name, a.project_id, COALESCE(p.name,'') FROM agents a LEFT JOIN projects p ON p.id=a.project_id
		WHERE a.provider_id=? AND a.archived_at=0 ORDER BY COALESCE(p.name,''), a.name, a.id`, providerID)
	if err != nil {
		l.c.logf("local models: the agents that use the server: %v", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var u LocalUser
		if rows.Scan(&u.AgentID, &u.Name, &u.ProjectID, &u.ProjectName) == nil {
			out = append(out, u)
		}
	}
	return out
}
