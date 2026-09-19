package exit

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"net"
)

// SOCKS5 protocol constants (RFC 1928 / 1929).
const (
	socks5Version = 0x05

	socks5AuthNone         = 0x00
	socks5AuthUserPass     = 0x02
	socks5AuthNoAcceptable = 0xff

	socks5CmdConnect      = 0x01
	socks5CmdBind         = 0x02
	socks5CmdUDPAssociate = 0x03

	socks5AtypIPv4   = 0x01
	socks5AtypDomain = 0x03
	socks5AtypIPv6   = 0x04

	socks5RepSuccess             = 0x00
	socks5RepGeneralFailure      = 0x01
	socks5RepNotAllowed          = 0x02
	socks5RepHostUnreachable     = 0x04
	socks5RepCommandNotSupported = 0x07
	socks5RepTTLExpired          = 0x06
)

// socks5Handshake performs the SOCKS5 method negotiation. It accepts either
// no-auth or username/password auth. When username/password is offered, any
// non-empty username is accepted and returned as the source tag (the password
// is ignored). Returns the source tag (may be empty).
func socks5Handshake(client net.Conn, br *bufio.Reader) (sourceTag string, err error) {
	ver, err := br.ReadByte()
	if err != nil {
		return "", err
	}
	if ver != socks5Version {
		return "", errors.New("not socks5")
	}
	nMethods, err := br.ReadByte()
	if err != nil {
		return "", err
	}
	methods := make([]byte, nMethods)
	if _, err := io.ReadFull(br, methods); err != nil {
		return "", err
	}
	offersUserPass := false
	offersNone := false
	for _, m := range methods {
		switch m {
		case socks5AuthUserPass:
			offersUserPass = true
		case socks5AuthNone:
			offersNone = true
		}
	}

	// Prefer username/password so we can capture the source tag.
	if offersUserPass {
		if _, err := client.Write([]byte{socks5Version, socks5AuthUserPass}); err != nil {
			return "", err
		}
		return socks5ReadUserPass(client, br)
	}
	if offersNone {
		if _, err := client.Write([]byte{socks5Version, socks5AuthNone}); err != nil {
			return "", err
		}
		return "", nil
	}
	_, _ = client.Write([]byte{socks5Version, socks5AuthNoAcceptable})
	return "", errors.New("no acceptable socks5 auth method")
}

// socks5ReadUserPass reads an RFC 1929 username/password and accepts it. The
// username is returned as the source tag.
func socks5ReadUserPass(client net.Conn, br *bufio.Reader) (string, error) {
	ver, err := br.ReadByte()
	if err != nil {
		return "", err
	}
	if ver != 0x01 {
		return "", errors.New("bad auth version")
	}
	ulen, err := br.ReadByte()
	if err != nil {
		return "", err
	}
	user := make([]byte, ulen)
	if _, err := io.ReadFull(br, user); err != nil {
		return "", err
	}
	plen, err := br.ReadByte()
	if err != nil {
		return "", err
	}
	pass := make([]byte, plen)
	if _, err := io.ReadFull(br, pass); err != nil {
		return "", err
	}
	// Accept any credentials; the username is an accounting tag, not a secret.
	if _, err := client.Write([]byte{0x01, 0x00}); err != nil {
		return "", err
	}
	return string(user), nil
}

// socks5ReadRequest reads a SOCKS5 request and returns the target host, port,
// and command.
func socks5ReadRequest(br *bufio.Reader) (host string, port, cmd int, err error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(br, header); err != nil {
		return "", 0, 0, err
	}
	if header[0] != socks5Version {
		return "", 0, 0, errors.New("bad socks5 version in request")
	}
	cmd = int(header[1])
	switch header[3] {
	case socks5AtypIPv4:
		addr := make([]byte, 4)
		if _, err := io.ReadFull(br, addr); err != nil {
			return "", 0, 0, err
		}
		host = net.IP(addr).String()
	case socks5AtypIPv6:
		addr := make([]byte, 16)
		if _, err := io.ReadFull(br, addr); err != nil {
			return "", 0, 0, err
		}
		host = net.IP(addr).String()
	case socks5AtypDomain:
		l, err := br.ReadByte()
		if err != nil {
			return "", 0, 0, err
		}
		name := make([]byte, l)
		if _, err := io.ReadFull(br, name); err != nil {
			return "", 0, 0, err
		}
		host = string(name)
	default:
		return "", 0, 0, errors.New("unknown address type")
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(br, portBytes); err != nil {
		return "", 0, 0, err
	}
	port = int(binary.BigEndian.Uint16(portBytes))
	return host, port, cmd, nil
}

// socks5Reply writes a SOCKS5 reply with the given reply code and a zero bound
// address.
func socks5Reply(client net.Conn, rep byte) error {
	// VER REP RSV ATYP=IPv4 BND.ADDR(0.0.0.0) BND.PORT(0)
	_, err := client.Write([]byte{socks5Version, rep, 0x00, socks5AtypIPv4, 0, 0, 0, 0, 0, 0})
	return err
}
