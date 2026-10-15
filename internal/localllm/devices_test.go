package localllm_test

import (
	"context"
	"os"
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
}

// The stand-in answers --list-devices like the real program; a program that does not answer leaves nothing known.
func TestDevicesAreProbedThroughTheProgram(t *testing.T) {
	pm := proc.New()
	d := localllm.ListDevices(context.Background(), pm, os.Args[0], llamatest.Args("--devices=cuda"))
	if !d.Probed || len(d.Devices) != 1 || d.Devices[0].Kind != "CUDA" || d.Devices[0].Free != 14336<<20 {
		t.Fatalf("%+v", d)
	}
	d = localllm.ListDevices(context.Background(), pm, os.Args[0], llamatest.Args("--devices=none"))
	if !d.Probed || len(d.Devices) != 0 {
		t.Fatalf("%+v", d)
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
