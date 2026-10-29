package localllm_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/localllm"
	"github.com/kjmaguire/drydock/internal/localllm/llamatest"
	"github.com/kjmaguire/drydock/internal/proc"
)

func TestTheDeviceListIsRead(t *testing.T) {
	out := "ggml_cuda_init: found 1 CUDA devices:\nAvailable devices:\n  CUDA0: NVIDIA GeForce RTX 4060 Ti (15944 MiB, 14143 MiB free)\n  Vulkan1: AMD Radeon 780M (2 GiB, 1 GiB free)\nload_backend: done\n"
	d, ok := localllm.ParseDevices(out)
	if !ok || !d.Probed || len(d.Devices) != 2 {
		t.Fatalf("%+v %v", d, ok)
	}
	if d.Devices[0].ID != "CUDA0" || d.Devices[0].Kind != "CUDA" || d.Devices[0].Name != "NVIDIA GeForce RTX 4060 Ti" || d.Devices[0].Total != 15944<<20 || d.Devices[0].Free != 14143<<20 {
		t.Fatalf("%+v", d.Devices[0])
	}
	if d.Devices[1].Kind != "Vulkan" || d.Devices[1].Total != 2<<30 {
		t.Fatalf("%+v", d.Devices[1])
	}
	d, ok = localllm.ParseDevices("Available devices:\n  (none)\n")
	if !ok || !d.Probed || len(d.Devices) != 0 {
		t.Fatalf("none: %+v", d)
	}
	if _, ok := localllm.ParseDevices("error: unknown argument"); ok {
		t.Fatal("garbage parsed")
	}
	// a device printed without its memory is still a device, and the CPU is no GPU
	d, _ = localllm.ParseDevices("Available devices:\n  Vulkan0: AMD Radeon RX 7800 XT\n  CPU: AMD Ryzen 9 7950X (65536 MiB, 60000 MiB free)\n")
	if len(d.Devices) != 2 || d.Devices[0].Kind != "Vulkan" || d.Devices[0].Name != "AMD Radeon RX 7800 XT" || d.Devices[0].Total != 0 || d.Devices[1].Kind != "CPU" {
		t.Fatalf("%+v", d)
	}
	if !d.HasGPU() {
		t.Fatal("a Vulkan device is a GPU")
	}
	d, _ = localllm.ParseDevices("Available devices:\n  CPU: AMD Ryzen 9 7950X (65536 MiB, 60000 MiB free)\n")
	if d.HasGPU() {
		t.Fatal("the CPU alone is no GPU")
	}
}

// The adapters Windows reports: Microsoft's fallback display drivers are no GPU, and each card is named once.
func TestDisplayAdaptersKeepTheGPUs(t *testing.T) {
	got := localllm.Adapters([]string{" NVIDIA GeForce RTX 4070 ", "Microsoft Basic Display Adapter", "", "Microsoft Remote Display Adapter", "NVIDIA GeForce RTX 4070", "AMD Radeon(TM) Graphics"})
	if strings.Join(got, "|") != "NVIDIA GeForce RTX 4070|AMD Radeon(TM) Graphics" {
		t.Fatalf("%q", got)
	}
	if localllm.Adapters(nil) != nil {
		t.Fatal("none from none")
	}
}

// The stand-in answers --list-devices like the real program; a program that does not answer leaves nothing known.
func TestDevicesAreProbedThroughTheProgram(t *testing.T) {
	pm := proc.New()
	d := localllm.ListDevices(context.Background(), pm, os.Args[0], llamatest.Args("--devices=cuda"))
	if !d.Probed || len(d.Devices) != 1 || d.Devices[0].Kind != "CUDA" || d.Devices[0].Free != 14336<<20 {
		t.Fatalf("%+v", d)
	}
	if d.Note != "" {
		t.Fatalf("a build that found its GPU has nothing to explain: %q", d.Note)
	}
	d = localllm.ListDevices(context.Background(), pm, os.Args[0], llamatest.Args("--devices=none"))
	if !d.Probed || len(d.Devices) != 0 {
		t.Fatalf("%+v", d)
	}
	// no GPU listed: what the program said about its backends is kept, in its words
	if d.Note != "ggml_vulkan: No devices found.\nload_backend: loaded CPU backend from stand-in-cpu.dll" {
		t.Fatalf("note %q", d.Note)
	}
	d = localllm.ListDevices(context.Background(), pm, os.Args[0], llamatest.Args("--devices=fail"))
	if d.Probed || d.Error == "" {
		t.Fatalf("%+v", d)
	}
}

func TestTheFitRule(t *testing.T) {
	gpu := localllm.Devices{Probed: true, Devices: []localllm.Device{{ID: "CUDA0", Kind: "CUDA", Free: 14 << 30}}}
	ram := localllm.Memory{Total: 64 << 30, Available: 40 << 30, Known: true}
	for _, c := range []struct {
		size uint64
		d    localllm.Devices
		m    localllm.Memory
		want string
		fits bool
	}{
		{10 << 30, gpu, ram, "gpu", true},
		{20 << 30, gpu, ram, "cpu", true}, // 30 GB budget
		{35 << 30, gpu, ram, "none", false},
		{20 << 30, localllm.Devices{Probed: true}, ram, "cpu", true},
		{200 << 30, localllm.Devices{}, localllm.Memory{}, "unknown", true},
		{10 << 30, localllm.Devices{}, ram, "cpu", true},
		// a GPU but no RAM reading: what does not fit the GPU may still run partly offloaded, so it is unknown, not too big
		{10 << 30, gpu, localllm.Memory{}, "gpu", true},
		{20 << 30, gpu, localllm.Memory{}, "unknown", true},
	} {
		f := localllm.FitOf(c.size, c.d, c.m)
		if f.Target != c.want || f.Fits != c.fits {
			t.Errorf("%d GB on %+v: %+v", c.size>>30, c.d.Devices, f)
		}
	}
	if m := localllm.SystemMemory(); m.Known && (m.Total == 0 || m.Available > m.Total) {
		t.Fatalf("%+v", m)
	}
}
