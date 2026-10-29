//go:build !linux && !windows

package peer

import "net/netip"

func owner(netip.AddrPort, netip.AddrPort) (int, error) { return 0, ErrUnsupported }
