//go:build linux

package wireguard

// defaultTUNName is the default TUN device name for Linux.
// The %d will be replaced with a number by the kernel.
const defaultTUNName = "ghost%d"
