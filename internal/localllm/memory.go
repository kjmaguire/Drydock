package localllm

// Memory is the PC's RAM.
type Memory struct {
	Total     uint64 `json:"totalBytes"`
	Available uint64 `json:"availableBytes"`
	Known     bool   `json:"known"`
}

// Fit says whether a model file of a size can be loaded here.
type Fit struct {
	// Target is where it would run: gpu (fits the free VRAM of one device), cpu (fits three quarters of the free RAM; with a
	// GPU this is a partial offload), none (too big), or unknown (nothing could be measured).
	Target string `json:"target"`
	Fits   bool   `json:"fits"`
	// Available is the larger of the free VRAM and the RAM budget, so the page can say how much there is.
	Available uint64 `json:"availableBytes"`
}

// RAMBudgetFraction of the available RAM a model may take: the rest is for Windows, Drydock and the server itself.
const RAMBudgetFraction = 0.75

// FitOf applies the rule. A model that fits in the free memory of one GPU runs there; one that fits three quarters of the
// free RAM runs on the CPU (partly offloaded when there is a GPU); the rest does not fit. With no device probed and no RAM
// reading the answer is unknown, and unknown counts as fitting (the person decides).
func FitOf(size uint64, d Devices, m Memory) Fit {
	var vram uint64
	for _, dev := range d.Devices {
		if dev.Kind != "CPU" && dev.Free > vram {
			vram = dev.Free
		}
	}
	ram := uint64(float64(m.Available) * RAMBudgetFraction)
	f := Fit{Available: max(vram, ram)}
	switch {
	case vram > 0 && size <= vram:
		f.Target, f.Fits = "gpu", true
	case !m.Known && vram == 0:
		f.Target, f.Fits = "unknown", true
	case m.Known && size <= ram:
		f.Target, f.Fits = "cpu", true
	default:
		f.Target = "none"
	}
	return f
}
