// Package localllm runs language models on the person's own PC with llama.cpp (decision P65, docs/local-models.md). Drydock
// downloads an official llama.cpp build for Windows from its GitHub release, checks it against the SHA-256 digest GitHub
// records for the asset, unpacks it in its own folder, downloads GGUF model files from Hugging Face (checked against the
// SHA-256 the Hub records for each file), and starts llama-server on the loopback address with one model at a time. The
// server is then an ordinary provider (kind host, flavor llamacpp) that the router and the agents use like any other.
//
// Nothing here runs a program it did not check, and nothing is downloaded from anywhere but the release downloads of the
// chosen source and huggingface.co. The TurboQuant fork is offered as a second source a person may opt into; it is a
// community build Drydock has not reviewed, and the page says so.
package localllm

import (
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// Backend is the compute backend of a llama.cpp build.
type Backend string

const (
	CPU        Backend = "cpu"
	Vulkan     Backend = "vulkan"
	CUDA       Backend = "cuda"        // the newest CUDA the source builds for (NVIDIA driver 580 or newer)
	CUDALegacy Backend = "cuda-legacy" // CUDA 12.4 (older NVIDIA drivers)
	ROCm       Backend = "rocm"
)

// Backends in the order the page offers them.
var Backends = []Backend{Vulkan, CUDA, CUDALegacy, ROCm, CPU}

// BackendInfo describes a backend for the page.
type BackendInfo struct {
	ID    Backend `json:"id"`
	Label string  `json:"label"`
	For   string  `json:"for"`
}

// Describe says what each backend is for.
func Describe(b Backend) BackendInfo {
	switch b {
	case Vulkan:
		return BackendInfo{ID: b, Label: "Vulkan", For: "Any recent GPU (AMD, Intel, NVIDIA) through its Vulkan driver"}
	case CUDA:
		return BackendInfo{ID: b, Label: "CUDA", For: "NVIDIA GPUs with a driver from 2025 or newer (CUDA 13)"}
	case CUDALegacy:
		return BackendInfo{ID: b, Label: "CUDA 12.4", For: "NVIDIA GPUs with an older driver"}
	case ROCm:
		return BackendInfo{ID: b, Label: "ROCm", For: "Recent AMD GPUs through ROCm (HIP)"}
	default:
		return BackendInfo{ID: CPU, Label: "CPU only", For: "No GPU: slow, works everywhere"}
	}
}

// Valid reports whether b is a backend Drydock offers.
func (b Backend) Valid() bool {
	for _, k := range Backends {
		if b == k {
			return true
		}
	}
	return false
}

// Source is where the llama.cpp build comes from: a GitHub repository whose releases carry Windows x64 archives.
type Source struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Repo is the GitHub repository ("owner/name").
	Repo string `json:"repo"`
	// Warning is shown before a person picks a source that is not the official one.
	Warning string `json:"warning,omitempty"`
	// CacheTypes are the KV cache types the build supports, the recommended one first.
	CacheTypes []string `json:"cacheTypes"`
	// Prerelease says whether a release marked pre-release counts (the official weekly builds are marked so).
	Prerelease bool `json:"-"`
	// assets names the archive of a backend for a release tag as a pattern in path.Match syntax ("" when the source has no
	// build for the backend): a `*` stands where the source writes a toolkit version (CUDA 13.4 today, 13.5 tomorrow), so
	// a toolkit bump upstream does not strand the backend. Where several assets of a release match, the newest toolkit (the
	// highest version in the name) is taken.
	assets func(tag string, b Backend) string
	// cudart names the CUDA runtime archive the archive needs beside it, from the archive's own name ("" when none).
	cudart func(archive string) string
	// releaseTag is how the source tags a release (its other tags are dev builds), with the parts that order releases as
	// submatches, most significant first; tagForm says it in words for messages.
	releaseTag *regexp.Regexp
	order      []int // the submatch indexes of releaseTag, most significant first
	tagForm    string
}

// toolkitVersion is the toolkit version in an archive's name ("cuda-13.4" is 13, 4), nil when it carries none.
var toolkitVersionR = regexp.MustCompile(`-(?:cuda|rocm)-(\d+(?:\.\d+)*)`)

func toolkitVersion(name string) []int {
	m := toolkitVersionR.FindStringSubmatch(name)
	if m == nil {
		return nil
	}
	var v []int
	for _, p := range strings.Split(m[1], ".") {
		n, _ := strconv.Atoi(p)
		v = append(v, n)
	}
	return v
}

// newerToolkit reports whether a's toolkit version is above b's (a name without one is the oldest).
func newerToolkit(a, b []int) bool {
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// officialCudart is the CUDA runtime archive beside an official CUDA archive: the same toolkit version.
func officialCudart(archive string) string {
	m := toolkitVersionR.FindStringSubmatch(archive)
	if m == nil || !strings.Contains(archive, "-cuda-") {
		return ""
	}
	return "cudart-llama-bin-win-cuda-" + m[1] + "-x64.zip"
}

// DownloadPrefix is the only place this source's archives are fetched from.
func (s Source) DownloadPrefix() string {
	return "https://github.com/" + s.Repo + "/releases/download/"
}

// releasesPerPage is the page size of the release listing (GitHub's largest).
const releasesPerPage = 100

// ReleasesPath is the REST path of one page of the source's releases, newest first.
func (s Source) ReleasesPath(page int) string {
	return fmt.Sprintf("/repos/%s/releases?per_page=%d&page=%d", s.Repo, releasesPerPage, page)
}

// ReleaseTag reports whether tag is one of the source's release tags (not a dev build): only those are installed.
func (s Source) ReleaseTag(tag string) bool {
	return s.releaseTag != nil && s.releaseTag.MatchString(tag)
}

// TagForm is the shape of the source's release tags, for messages ("bNNNN-X.Y.Z").
func (s Source) TagForm() string { return s.tagForm }

// version is the release tag's ordering key, or nil when tag is not a release tag of the source.
func (s Source) version(tag string) []int {
	if s.releaseTag == nil {
		return nil
	}
	m := s.releaseTag.FindStringSubmatch(tag)
	if m == nil {
		return nil
	}
	v := make([]int, 0, len(s.order))
	for _, i := range s.order {
		n, _ := strconv.Atoi(m[i])
		v = append(v, n)
	}
	return v
}

// Newer reports whether release a is newer than release b by the source's own numbering (for the fork, its version before
// the llama.cpp build it is based on, so a late backport to an older line is not newer). known is false when either tag is
// not a release tag of the source; the caller then cannot order them.
func (s Source) Newer(a, b string) (newer, known bool) {
	va, vb := s.version(a), s.version(b)
	if va == nil || vb == nil {
		return false, false
	}
	return newerToolkit(va, vb), true
}

// Supports reports whether the source builds for the backend.
func (s Source) Supports(b Backend) bool { return s.assets("tag", b) != "" }

// CacheType reports whether the KV cache type is one this source's build accepts.
func (s Source) CacheType(t string) bool {
	for _, k := range s.CacheTypes {
		if k == t {
			return true
		}
	}
	return false
}

const (
	// SourceOfficial is ggml-org's own weekly build.
	SourceOfficial = "official"
	// SourceTurboQuant is the community fork with Google's TurboQuant KV cache types.
	SourceTurboQuant = "turboquant"
)

// Sources in the order the page offers them: the official build first.
var Sources = []Source{
	{
		ID: SourceOfficial, Name: "Official weekly build", Repo: "ggml-org/llama.cpp",
		CacheTypes: []string{"q8_0", "q4_0", "f16"}, Prerelease: true,
		assets: func(tag string, b Backend) string {
			switch b {
			case CPU:
				return fmt.Sprintf("llama-%s-bin-win-cpu-x64.zip", tag)
			case Vulkan:
				return fmt.Sprintf("llama-%s-bin-win-vulkan-x64.zip", tag)
			case CUDA: // CUDA 13 and later (the toolkit's minor changes with the driver generation upstream builds for)
				return fmt.Sprintf("llama-%s-bin-win-cuda-1[3-9].*-x64.zip", tag)
			case CUDALegacy:
				return fmt.Sprintf("llama-%s-bin-win-cuda-12.*-x64.zip", tag)
			case ROCm:
				return fmt.Sprintf("llama-%s-bin-win-rocm-*-x64.zip", tag)
			}
			return ""
		},
		cudart:     officialCudart,
		releaseTag: regexp.MustCompile(`^b(\d+)$`), order: []int{1}, tagForm: "bNNNN",
	},
	{
		ID: SourceTurboQuant, Name: "TurboQuant build", Repo: "AtomicBot-ai/atomic-llama-cpp-turboquant",
		Warning: "A community fork of llama.cpp that Drydock has not reviewed. It adds Google's TurboQuant KV cache types (turbo3, turbo4) " +
			"but trails the official build by weeks to months. Drydock checks the archive against the SHA-256 GitHub recorded " +
			"when the maintainer uploaded it, nothing more. Use it only if you trust its maintainers.",
		CacheTypes: []string{"turbo4", "turbo3", "q8_0", "q4_0", "f16"},
		assets: func(_ string, b Backend) string {
			switch b {
			case CPU:
				return "llama-turboquant-windows-x64-cpu.zip"
			case Vulkan:
				return "llama-turboquant-windows-x64-vulkan.zip"
			case CUDA:
				return "llama-turboquant-windows-x64-cuda-1[3-9].*.zip"
			case CUDALegacy:
				return "llama-turboquant-windows-x64-cuda-12.*.zip"
			case ROCm:
				return "llama-turboquant-windows-x64-rocm*.zip"
			}
			return ""
		},
		cudart: func(string) string { return "" },
		// its releases are b<llama.cpp build>-<its own version> (b10298-2.0.0); its per-commit builds
		// (turboquant-windows-x64-vulkan-519f0c5) and dev-latest are pre-release dev builds
		releaseTag: regexp.MustCompile(`^b(\d+)-(\d+)\.(\d+)\.(\d+)$`), order: []int{2, 3, 4, 1}, tagForm: "bNNNN-X.Y.Z",
	},
}

// SourceByID finds a source.
func SourceByID(id string) (Source, bool) {
	for _, s := range Sources {
		if s.ID == id {
			return s, true
		}
	}
	return Source{}, false
}

// ServerName is the file name of llama.cpp's server program on this operating system.
func ServerName() string {
	if runtime.GOOS == "windows" {
		return "llama-server.exe"
	}
	return "llama-server"
}

// isServer reports whether a file name is the server program (either operating system's name).
func isServer(name string) bool {
	return strings.EqualFold(name, "llama-server.exe") || name == "llama-server"
}
