//go:build !windows

package localllm

// DisplayAdapters lists the display adapters Windows has a driver for; elsewhere there is none to read.
func DisplayAdapters() []string { return nil }
