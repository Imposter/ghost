package wireguard

import (
	"fmt"

	"golang.zx2c4.com/wireguard/tun"
)

// CreateTUN creates a TUN interface with the given name and MTU.
// This works for desktop platforms (Linux, Windows, macOS).
// For mobile platforms, use CreateTUNFromFD instead.
func CreateTUN(name string, mtu int) (tun.Device, error) {
	if mtu <= 0 {
		return nil, fmt.Errorf("invalid MTU: %d", mtu)
	}

	if name == "" {
		name = getDefaultTUNName()
	}

	// wireguard-go handles platform differences internally
	tunnel, err := tun.CreateTUN(name, mtu)
	if err != nil {
		return nil, fmt.Errorf("failed to create TUN interface: %w", err)
	}

	return tunnel, nil
}

// CreateTUNFromFD creates a TUN interface from an existing file descriptor.
// This is used for mobile platforms where the OS creates the TUN interface:
//   - Android: VpnService.Builder.establish() creates TUN, passes fd to Go
//   - iOS: Not applicable - iOS uses packet flow handlers (see Phase 5)
//
// The mobile app creates the TUN via OS APIs, then passes the fd here.
func CreateTUNFromFD(fd int) (tun.Device, error) {
	// TODO: Implement for Android in Phase 5
	// Android will pass the fd from VpnService.Builder.establish()
	return nil, fmt.Errorf("CreateTUNFromFD not yet implemented - needed for Android in Phase 5")
}

// getDefaultTUNName returns a platform-specific default name.
func getDefaultTUNName() string {
	// Platform-specific implementations provide the actual name
	return defaultTUNName
}
