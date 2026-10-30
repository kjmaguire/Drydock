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

// DraftMTP is the draft a person chooses for a model that drafts with its own MTP heads (decision P120): it stands in the
// settings where a draft model's id would (a model's id always holds a ":", so no model is called so).
const DraftMTP = "mtp"

// DraftFits says whether draft can draft for target in speculative decoding (llama-server --model-draft): a model of the same
// family with the same tokenizer (kind, size within llama.cpp's slack, the same first and last tokens), at most half the
// target's size, neither of them an embedding model. An EAGLE-3 draft (decision P120) is of no family: it was converted for one
// target, whose tokenizer it inherits (llama.cpp's convert_hf_to_gguf.py --target-model-dir) and whose embedding length it
// names when the file gives it. A DFlash or DSpark draft (decision P129) is of no family either, for the same reason, and reads
// layers of its model up to the one it names (dflash.target_layers), which the model must have. An MTP head shipped as a file
// of its own (decision P129) is the model's own architecture, embedding length and layers: llama.cpp loads it beside the model
// and runs its MTP blocks against the model's hidden states. A Gemma 4 assistant (decision P132) drafts for a Gemma 4 model
// whose hidden state is as wide as the one it names (embedding_length_out), with its tokenizer; an MTP file of the TurboQuant
// fork's older layouts drafts for nothing, since neither build loads it. When it cannot, the reason says why in a sentence.
func DraftFits(target, draft GGUFInfo, targetBytes, draftBytes uint64) (bool, string) {
	kind := draft.DraftOnly()
	switch {
	case LooksLikeEmbedding(target) || LooksLikeEmbedding(draft):
		return false, "an embedding model neither drafts nor is drafted for"
	case target.DraftOnly() != "":
		return false, anOf(DraftKindName(target.DraftOnly())) + " is drafted for by nothing"
	case kind == DraftOnlyOldMTP:
		return false, "it is an MTP file made for an older TurboQuant build, which no engine Drydock installs loads"
	case kind == DraftOnlyAssistant && target.Architecture != Gemma4:
		return false, fmt.Sprintf("it is a Gemma 4 assistant, and the model is %s", target.Architecture)
	case kind == DraftOnlyAssistant && embdOut(draft) != embdOut(target):
		return false, fmt.Sprintf("it is a Gemma 4 assistant for a model of %d dimensions, and the model has %d", embdOut(draft), embdOut(target))
	case draft.Eagle3() && draft.TargetHiddenSize > 0 && draft.TargetHiddenSize != target.EmbeddingLength:
		return false, fmt.Sprintf("it is an EAGLE-3 draft for a model of %d dimensions, and the model has %d", draft.TargetHiddenSize, target.EmbeddingLength)
	case (kind == DraftOnlyDFlash || kind == DraftOnlyDSpark) && draft.TargetLayer > target.BlockCount:
		return false, fmt.Sprintf("it is %s that reads layer %d of its model, and the model has %d", anOf(DraftKindName(kind)), draft.TargetLayer, target.BlockCount)
	case kind == DraftOnlyMTPHead && (draft.Architecture != target.Architecture || draft.EmbeddingLength != target.EmbeddingLength ||
		mainLayers(draft) != mainLayers(target)):
		return false, fmt.Sprintf("it is an MTP head for a %s model of %d layers and %d dimensions, and the model is %s with %d and %d",
			draft.Architecture, mainLayers(draft), draft.EmbeddingLength, target.Architecture, mainLayers(target), target.EmbeddingLength)
	case target.Architecture == "" || (kind == "" && family(target.Architecture) != family(draft.Architecture)):
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

// DraftKindName is a draft-only kind in words, for messages: "EAGLE-3 draft", "DFlash draft", "DSpark draft", "MTP head".
func DraftKindName(kind string) string {
	switch kind {
	case DraftOnlyEagle3:
		return "EAGLE-3 draft"
	case DraftOnlyDFlash:
		return "DFlash draft"
	case DraftOnlyDSpark:
		return "DSpark draft"
	case DraftOnlyMTPHead:
		return "MTP head"
	case DraftOnlyAssistant:
		return "Gemma 4 assistant"
	case DraftOnlyOldMTP:
		return "old MTP file"
	}
	return "draft"
}

// DraftKindPhrase is a draft-only kind with its article, for messages: "an EAGLE-3 draft", "a DSpark draft".
func DraftKindPhrase(kind string) string { return anOf(DraftKindName(kind)) }

// anOf is a kind's name with its article, as it is said: "an EAGLE-3 draft", "an MTP head", "a DFlash draft".
func anOf(name string) string {
	if strings.HasPrefix(name, "EAGLE") || strings.HasPrefix(name, "MTP") || strings.HasPrefix(name, "old") {
		return "an " + name
	}
	return "a " + name
}
