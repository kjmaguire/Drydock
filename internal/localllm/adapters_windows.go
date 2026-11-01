//go:build windows

package localllm

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// displayClass is the device setup class of display adapters; each adapter with a driver is a numbered subkey ("0000").
const displayClass = `SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`

// DisplayAdapters lists the display adapters Windows has a driver for, by the driver's description, read from the registry
// (no program is started). Microsoft's own fallbacks (Basic Display, Remote Display, Hyper-V) are left out: they are no GPU.
func DisplayAdapters() []string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, displayClass, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	defer k.Close()
	subs, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}
	var names []string
	for _, sub := range subs {
		if len(sub) != 4 || strings.Trim(sub, "0123456789") != "" {
			continue // "Properties", "Configuration"
		}
		a, err := registry.OpenKey(k, sub, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		desc, _, err := a.GetStringValue("DriverDesc")
		a.Close()
		if err == nil {
			names = append(names, desc)
		}
	}
	return Adapters(names)
}
