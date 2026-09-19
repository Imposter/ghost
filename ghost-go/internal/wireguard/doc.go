// Package wireguard wraps wireguard-go for ghost: key handling, a Tunnel with
// dynamic peer management, and a userspace netstack TUN (CreateNetTUN) that
// only accepts packets addressed to its own tunnel addresses.
package wireguard
