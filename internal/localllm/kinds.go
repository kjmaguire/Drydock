package localllm

import (
	"fmt"
	"strings"
)

// What a downloaded model is for, read from its GGUF header (decision P111): a text model the server chats with, an embedding
// model (served on its own, with --embeddings), and whether a smaller model can draft for a larger one in speculative decoding.

// embeddingArchs are the architectures llama.cpp has for encoder models, which make embeddings and never chat.
var embeddingArchs = map[string]bool{"bert": true, "nomic-bert": true, "nomic-bert-moe": true, "jina-bert-v2": true, "jina-bert-v3": true,
	"t5encoder": true, "modern-bert": true, "neo-bert": true, "eurobert": true}

// LooksLikeEmbedding is what the header says about a model: an encoder architecture, or a pooling type (which the converter
// writes for a model made for embeddings, Qwen3-Embedding among them). A person may say otherwise on the page.
func LooksLikeEmbedding(info GGUFInfo) bool {
	return info.Pooling || embeddingArchs[info.Architecture]
}

// vocabSlack is how many tokens a draft's vocabulary may differ from the target's: llama.cpp's own limit
// (SPEC_VOCAB_MAX_SIZE_DIFFERENCE), past which it refuses the pair.
const vocabSlack = 128

// family is an architecture without its mixture-of-experts suffix: a dense Qwen3 drafts for a Qwen3 MoE.
func family(arch string) string { return strings.TrimSuffix(arch, "moe") }

// DraftFits says whether draft can draft for target in speculative decoding (llama-server --model-draft): a model of the same
// family with the same tokenizer (kind, size within llama.cpp's slack, the same first and last tokens), at most half the
// target's size, neither of them an embedding model. When it cannot, the reason says why in a sentence.
func DraftFits(target, draft GGUFInfo, targetBytes, draftBytes uint64) (bool, string) {
	switch {
	case LooksLikeEmbedding(target) || LooksLikeEmbedding(draft):
		return false, "an embedding model neither drafts nor is drafted for"
	case target.Architecture == "" || family(target.Architecture) != family(draft.Architecture):
		return false, fmt.Sprintf("it is a %s model and the model is %s: a draft must be of the same family", draft.Architecture, target.Architecture)
	case target.Tokenizer == "" || target.Tokenizer != draft.Tokenizer || target.VocabSize <= 0 || draft.VocabSize <= 0:
		return false, "its tokenizer is not the model's"
	case target.VocabSize-draft.VocabSize > vocabSlack || draft.VocabSize-target.VocabSize > vocabSlack:
		return false, fmt.Sprintf("its vocabulary has %d tokens and the model's %d", draft.VocabSize, target.VocabSize)
	case target.BOS != draft.BOS || target.EOS != draft.EOS:
		return false, "its first and last tokens are not the model's"
	case draftBytes == 0 || draftBytes*2 > targetBytes:
		return false, "it is not much smaller than the model, so it would not speed it up"
	}
	return true, ""
}
