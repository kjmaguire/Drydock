package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/localllm"
)

// Several models, vision, drafts, embeddings and gated models on this PC (decision P111, docs/local-models.md): the person
// chooses which downloaded models the server keeps loaded (llama-server's router mode when there are more than one), within
// the memory the page estimates and at most localllm.MaxLoaded; a model with a projector reads images; a smaller model of the
// same family may draft for a larger one; an embedding model is served by a server of its own; and a Hugging Face token, kept
// in the credential store, lets a gated model download.

// hfTokenRef is the Hugging Face token's name in the secret store; hfTokenName its name for the redaction scanner.
const (
	hfTokenRef  = "localmodels:hf-token"
	hfTokenName = "huggingface-token"
)

// servePlan is what serve starts: the models in the order asked, the draft of each that has one, and the kind of speculative
// decoding of each that is not a plain draft model's (a file that only drafts, or the model's own MTP heads; decisions P120 and
// P129).
type servePlan struct {
	models []localllm.Model
	drafts map[string]localllm.Model
	specs  map[string]string
}

// heads says what a model's header says of draft heads that are not models (decisions P120 and P129): whether it carries its
// own MTP heads, and the kind of draft it is when it only drafts ("eagle3", "dflash", "dspark", "mtp-head"; "" for a model).
func (l *localExt) heads(m localllm.Model) (mtp bool, kind string) {
	info, err := l.ggufOf(m)
	if err != nil {
		return false, ""
	}
	return info.OwnMTP(), info.DraftOnly()
}

// mtpKVOf is the memory of the MTP draft context of a model that drafts with MTP heads (decision P129): its own (m) or a head
// file's (draft), at the model's context size and cache type; 0 when it cannot be estimated.
func (l *localExt) mtpKVOf(m localllm.Model, contextSize int, cache string) uint64 {
	info, err := l.ggufOf(m)
	if err != nil {
		return 0
	}
	if kv := localllm.EstimateMTPKV(info, contextSize, cache); kv.Known {
		return kv.Bytes
	}
	return 0
}

// isEmbedding is a model's kind: the person's mark, else what its header says.
func (l *localExt) isEmbedding(m localllm.Model) bool {
	if m.Mark != nil {
		return *m.Mark
	}
	info, err := l.ggufOf(m)
	return err == nil && localllm.LooksLikeEmbedding(info)
}

// draftOK says whether draft can draft for m, read from their headers.
func (l *localExt) draftOK(m, draft localllm.Model) bool {
	if m.ID == draft.ID || l.isEmbedding(m) || l.isEmbedding(draft) {
		return false
	}
	ti, err := l.ggufOf(m)
	if err != nil {
		return false
	}
	di, err := l.ggufOf(draft)
	if err != nil {
		return false
	}
	ok, _ := localllm.DraftFits(ti, di, m.Size, draft.Size)
	return ok
}

// needBytes is what a model takes when loaded: its files (and projector) and, when it could be estimated, its KV cache.
func needBytes(m localllm.Model, kv localllm.KVCache) uint64 {
	if kv.Known {
		return m.Size + kv.Bytes
	}
	return m.Size
}

// loadBudget is the memory several models loaded at once may take: the free memory of the GPU with the most, or three
// quarters of the free RAM, whichever is more (the fit rule's); 0 when nothing could be measured.
func loadBudget(d localllm.Devices, mem localllm.Memory) uint64 {
	return localllm.FitOf(0, d, mem).Available
}

// plan checks the models a start asks for: each downloaded once, a chat model, at most localllm.MaxLoaded of them, and, when
// there are several, all of them (with their drafts and KV caches) within the memory budget. A draft that is gone or no longer
// fits its model is left out, not refused: the model still runs, only slower.
func (l *localExt) plan(ctx context.Context, in localllm.Installed, s LocalSettings, ids []string, cache string) (servePlan, error) {
	p := servePlan{drafts: map[string]localllm.Model{}, specs: map[string]string{}}
	if len(ids) > localllm.MaxLoaded {
		return p, fmt.Errorf("at most %d models stay loaded at once", localllm.MaxLoaded)
	}
	seen := map[string]bool{}
	var total uint64
	for _, id := range ids {
		if seen[id] {
			return p, fmt.Errorf("%s is named twice", id)
		}
		seen[id] = true
		m, err := l.m.Store.Get(id)
		if err != nil {
			return p, err
		}
		if l.isEmbedding(m) {
			return p, fmt.Errorf("%s is an embedding model: it is served on its own, as the embedding model", id)
		}
		mtp, kind := l.heads(m)
		if kind == localllm.DraftOnlyOldMTP {
			return p, fmt.Errorf("%s is an MTP file made for an older TurboQuant build: no engine Drydock installs loads it", id)
		}
		if kind != "" {
			return p, fmt.Errorf("%s is %s: it does not chat, it drafts for the model it was made for (choose it as that model's draft)", id, localllm.DraftKindPhrase(kind))
		}
		p.models = append(p.models, m)
		total += needBytes(m, l.kvOf(m, s.ContextSize, cache))
		if did := s.Drafts[id]; did == localllm.DraftMTP {
			// the model's own heads draft: no file, and a context of their own whose cache holds the MTP layers (decision P129)
			if mtp {
				p.specs[id] = localllm.SpecMTP
				total += l.mtpKVOf(m, s.ContextSize, cache)
			} else {
				l.c.logf("local models: %s is served without MTP drafting (its file carries no MTP heads)", id)
			}
		} else if did != "" {
			if d, err := l.m.Store.Get(did); err == nil && l.draftOK(m, d) {
				p.drafts[id] = d
				if _, k := l.heads(d); k != "" {
					p.specs[id] = localllm.SpecOf(k)
				}
				// a head file's cache is its MTP context's (localllm.EstimateKV)
				total += needBytes(d, l.kvOf(d, s.ContextSize, cache))
			} else {
				l.c.logf("local models: %s is served without its draft %s (gone, or no longer one it can use)", id, did)
			}
		}
	}
	if len(p.models) > 1 {
		dev, mem := l.devicesOf(ctx, in), l.m.Memory()
		if f := localllm.FitOf(total, dev, mem); f.Target == "none" {
			return p, fmt.Errorf("these %d models need about %s together, and this PC has %s for them: keep fewer or smaller models loaded",
				len(p.models), gbWords(total), gbWords(f.Available))
		}
	}
	return p, nil
}

// fillModels completes the view's models: each one's KV cache and fit (at the settings the next start uses, or the ones the
// server runs it with), its kind, the models that can draft for it and the one chosen, what it takes loaded; the memory the
// served models take, and the room for more.
func (l *localExt) fillModels(v *LocalView, s LocalSettings) {
	cache, ctxSize := l.launchCache(s), s.ContextSize
	running := v.Server.State == "running" || v.Server.State == "starting"
	kvs := make([]localllm.KVCache, len(v.Models))
	byID := map[string]int{}
	for i := range v.Models {
		m := &v.Models[i]
		byID[m.ID] = i
		c, n := cache, ctxSize
		if running && v.Server.Serves(m.ID) {
			c, n = v.Server.CacheType, v.Server.ContextSize
		}
		kvs[i] = l.kvOf(*m, n, c)
		kv := kvs[i]
		m.KV = &kv
		f := localllm.FitWithKV(m.Size, &kv, v.Runtime.Devices, v.Memory)
		m.Fit = &f
		m.Embedding = l.isEmbedding(*m)
		m.MTP, m.DraftKind = l.heads(*m)
		m.Eagle3 = m.DraftKind == localllm.DraftOnlyEagle3
		v.ModelsBytes += m.Size
	}
	for i := range v.Models {
		m := &v.Models[i]
		m.LoadBytes = needBytes(*m, kvs[i])
		if m.Embedding || m.DraftKind != "" {
			continue
		}
		if m.MTP {
			m.Drafts = append(m.Drafts, localllm.DraftMTP)
		}
		for j := range v.Models {
			if i != j && l.draftOK(*m, v.Models[j]) {
				m.Drafts = append(m.Drafts, v.Models[j].ID)
			}
		}
		if d := s.Drafts[m.ID]; d != "" {
			for _, c := range m.Drafts {
				if c != d {
					continue
				}
				m.Draft = d
				if d == localllm.DraftMTP {
					// the MTP context, as long as the model's, with the model's cache type: in what it takes and in its fit
					m.MTPKVBytes = l.mtpKVOf(*m, kvs[i].ContextSize, kvs[i].CacheType)
					m.LoadBytes += m.MTPKVBytes
					f := localllm.FitWithKV(m.Size+m.MTPKVBytes, m.KV, v.Runtime.Devices, v.Memory)
					m.Fit = &f
				} else if j, ok := byID[d]; ok {
					m.LoadBytes += needBytes(v.Models[j], kvs[j])
					if v.Models[j].DraftKind == localllm.DraftOnlyMTPHead && kvs[j].Known {
						m.MTPKVBytes = kvs[j].Bytes
					}
				}
			}
		}
	}
	v.Load = LocalLoad{Max: localllm.MaxLoaded, BudgetBytes: loadBudget(v.Runtime.Devices, v.Memory)}
	if !running {
		return
	}
	for _, id := range served(v.Server) {
		i, ok := byID[id]
		if !ok {
			continue
		}
		m := v.Models[i]
		v.Load.LoadedBytes += m.LoadBytes
		if v.Served == nil {
			v.Served = servedOf(m, kvs[i], *m.Fit, v.Runtime.Devices, v.Memory)
			v.Served.ModelBytes, v.Served.KVBytes = 0, 0
		}
		v.Served.Models = append(v.Served.Models, id)
		v.Served.ModelBytes += m.Size
		if kvs[i].Known {
			v.Served.KVBytes += kvs[i].Bytes
		}
		if m.Draft == localllm.DraftMTP {
			// its own heads: no file, their context's cache (an MTP head file's is its draft's cache below)
			v.Served.KVBytes += m.MTPKVBytes
		} else if d, ok := byID[m.Draft]; ok {
			v.Served.ModelBytes += v.Models[d].Size
			if kvs[d].Known {
				v.Served.KVBytes += kvs[d].Bytes
			}
		}
	}
}

// served are the models a server state keeps loaded.
func served(st localllm.ServerState) []string {
	if len(st.Models) > 0 {
		return st.Models
	}
	if st.ModelID != "" {
		return []string{st.ModelID}
	}
	return nil
}

// forgetModel takes a removed model out of the settings; it reports whether anything changed.
func forgetModel(s *LocalSettings, id string) bool {
	changed := false
	if s.ModelID == id {
		s.ModelID, changed = "", true
	}
	kept := s.Loaded[:0:0]
	for _, m := range s.Loaded {
		if m != id {
			kept = append(kept, m)
		}
	}
	if len(kept) != len(s.Loaded) {
		s.Loaded, changed = kept, true
		if s.ModelID == "" && len(kept) > 0 {
			s.ModelID = kept[0]
		}
	}
	for target, draft := range s.Drafts {
		if target == id || draft == id {
			delete(s.Drafts, target)
			changed = true
		}
	}
	if s.EmbeddingModel == id {
		s.EmbeddingModel, changed = "", true
	}
	return changed
}

// setModel records a person's word on a downloaded model: whether it is an embedding model (it is then never served for chat
// and drafts for nothing), and the draft model it runs with ("" for none), which must be one that can draft for it. A change
// takes effect at the next start of the server.
func (l *localExt) setModel(ctx context.Context, id string, embedding *bool, draft *string, by string) (LocalView, error) {
	if !isPerson(by) {
		return LocalView{}, errors.New("only a person may change a model")
	}
	m, err := l.m.Store.Get(id)
	if err != nil {
		return LocalView{}, err
	}
	if embedding != nil {
		if *embedding && l.m.Server.State().Serves(id) {
			return LocalView{}, fmt.Errorf("%s is being served for chat: stop the server first", id)
		}
		if !*embedding && l.m.Embed.State().Serves(id) {
			return LocalView{}, fmt.Errorf("%s is being served for embeddings: stop it first", id)
		}
		if err := l.m.Store.Mark(id, embedding); err != nil {
			return LocalView{}, err
		}
		m.Mark = embedding
	}
	if err := l.update(ctx, func(s *LocalSettings) error {
		if draft != nil {
			if *draft == localllm.DraftMTP {
				// its own MTP heads (decision P120): only a model whose file carries them
				if mtp, _ := l.heads(m); !mtp {
					return fmt.Errorf("%s carries no MTP heads: its file has no multi-token prediction layers", id)
				}
				if s.Drafts == nil {
					s.Drafts = map[string]string{}
				}
				s.Drafts[id] = localllm.DraftMTP
			} else if *draft == "" {
				delete(s.Drafts, id)
			} else {
				d, err := l.m.Store.Get(*draft)
				if err != nil {
					return err
				}
				if !l.draftOK(m, d) {
					return fmt.Errorf("%s cannot draft for %s: a draft is a much smaller model of the same family, with the same tokenizer", *draft, id)
				}
				if s.Drafts == nil {
					s.Drafts = map[string]string{}
				}
				s.Drafts[id] = *draft
			}
		}
		if embedding != nil && *embedding {
			delete(s.Drafts, id) // an embedding model drafts for nothing and has no draft
			for target, d := range s.Drafts {
				if d == id {
					delete(s.Drafts, target)
				}
			}
		}
		return nil
	}); err != nil {
		return LocalView{}, err
	}
	l.changed("models", map[string]any{"id": id})
	return l.view(ctx), nil
}

// startEmbeddings serves an embedding model on the embedding server, a llama-server of its own at the embedding port.
func (l *localExt) startEmbeddings(ctx context.Context, modelID string, by string) (LocalView, error) {
	if !isPerson(by) {
		return LocalView{}, errors.New("only a person may start the embedding server")
	}
	if err := l.serveEmbeddings(ctx, modelID, false); err != nil {
		return LocalView{}, err
	}
	return l.view(ctx), nil
}

// serveEmbeddings starts the embedding server on a model marked (or read) as an embedding model. It holds a key of its own
// for every start, like the chat server, and is no provider: the router would offer it for chat, which it cannot answer.
func (l *localExt) serveEmbeddings(ctx context.Context, modelID string, auto bool) error {
	ctx = context.WithoutCancel(ctx)
	in, ok := l.m.Runtime.Installed()
	if !ok {
		return errors.New("no llama.cpp build is installed: install one first")
	}
	s := l.settings(ctx)
	m, err := l.m.Store.Get(modelID)
	if err != nil {
		return err
	}
	if !l.isEmbedding(m) {
		return fmt.Errorf("%s is not an embedding model: mark it as one first", modelID)
	}
	if s.EmbeddingPort == s.Port {
		return errors.New("the embedding server needs a port of its own")
	}
	l.mu.Lock()
	if l.embedStarting {
		l.mu.Unlock()
		return errors.New("the embedding server is already starting")
	}
	l.embedStarting = true
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.embedStarting = false
		l.mu.Unlock()
	}()
	prog, args := l.m.ServerProgram(in)
	launch := localllm.Launch{Program: prog, Args: args, Model: m, Embeddings: true, Port: s.EmbeddingPort, ContextSize: s.ContextSize,
		LogDir: filepath.Join(l.m.LogDir(), "embeddings"), APIKey: localllm.NewAPIKey(), Autostarted: auto}
	if err := l.m.Embed.Start(ctx, launch); err != nil {
		return err
	}
	if err := l.update(ctx, func(s *LocalSettings) error {
		s.EmbeddingModel = modelID
		return nil
	}); err != nil {
		return err
	}
	l.changed("embeddings", map[string]any{"state": "running", "modelId": modelID})
	return nil
}

func (l *localExt) stopEmbeddings(ctx context.Context, by string) (LocalView, error) {
	if !isPerson(by) {
		return LocalView{}, errors.New("only a person may stop the embedding server")
	}
	l.m.Embed.Stop()
	l.changed("embeddings", map[string]any{"state": "stopped"})
	return l.view(ctx), nil
}

// loadToken reads the Hugging Face token from the credential store and teaches the redaction scanner its value.
func (l *localExt) loadToken(ctx context.Context) {
	if l.c.secrets == nil {
		return
	}
	tok, err := l.c.secrets.Get(ctx, hfTokenRef)
	if err != nil {
		return // none yet (or the store cannot be read: the page then shows no token)
	}
	l.mu.Lock()
	l.hfToken = tok
	l.mu.Unlock()
	if l.c.Scanner != nil {
		_ = l.c.Scanner.RegisterSecret(hfTokenName, tok)
	}
}

// token is the Hugging Face token for the Hub's downloads ("" when there is none).
func (l *localExt) token() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.hfToken
}

// tokenView is the token as the page shows it: "hf_…" and its last four characters.
func (l *localExt) tokenView() LocalToken {
	tok := l.token()
	if tok == "" {
		return LocalToken{}
	}
	return LocalToken{Set: true, Masked: maskToken(tok)}
}

func maskToken(tok string) string {
	if len(tok) < 12 {
		return "…"
	}
	return tok[:3] + "…" + tok[len(tok)-4:]
}

// setToken keeps a person's Hugging Face token in the credential store (the vault: Credential Manager on Windows). It is read
// by nothing but the Hub's downloads, never logged, and shown masked.
func (l *localExt) setToken(ctx context.Context, tok string, by string) (LocalView, error) {
	if !isPerson(by) {
		return LocalView{}, errors.New("only a person may give Drydock a Hugging Face token")
	}
	tok = strings.TrimSpace(tok)
	if !strings.HasPrefix(tok, "hf_") || len(tok) < 12 || len(tok) > 256 || strings.ContainsAny(tok, " \t\r\n") {
		return LocalView{}, errors.New("a Hugging Face token starts with hf_ (make one at https://huggingface.co/settings/tokens, read access is enough)")
	}
	if l.c.secrets == nil {
		return LocalView{}, errNoVault
	}
	if err := l.c.secrets.Put(ctx, hfTokenRef, tok); err != nil {
		return LocalView{}, fmt.Errorf("could not keep the token in the credential store: %w", err)
	}
	l.mu.Lock()
	l.hfToken = tok
	l.mu.Unlock()
	if l.c.Scanner != nil {
		_ = l.c.Scanner.RegisterSecret(hfTokenName, tok)
	}
	l.changed("settings", map[string]any{"hfToken": true, "at": time.Now().UTC()})
	return l.view(ctx), nil
}

func (l *localExt) removeToken(ctx context.Context, by string) (LocalView, error) {
	if !isPerson(by) {
		return LocalView{}, errors.New("only a person may remove the Hugging Face token")
	}
	if l.c.secrets != nil {
		if err := l.c.secrets.Delete(ctx, hfTokenRef); err != nil {
			return LocalView{}, fmt.Errorf("could not remove the token from the credential store: %w", err)
		}
	}
	l.mu.Lock()
	l.hfToken = ""
	l.mu.Unlock()
	if l.c.Scanner != nil {
		l.c.Scanner.UnregisterSecret(hfTokenName)
	}
	l.changed("settings", map[string]any{"hfToken": false})
	return l.view(ctx), nil
}
