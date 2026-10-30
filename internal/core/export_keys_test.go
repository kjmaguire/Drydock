package core

import "maps"

// ShortcutDefaults is the shortcuts a person can bind, with their default keys (decision P132), for the test that checks them
// against the UI's keyboard map.
func ShortcutDefaults() map[string]string { return maps.Clone(shortcutDefaults) }

// ReservedCombos is the combinations no binding may take, in the core's order.
func ReservedCombos() []string { return append([]string(nil), reservedCombos...) }
