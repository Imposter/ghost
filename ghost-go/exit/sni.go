package exit

import "encoding/binary"

// sniffClientHelloSNI extracts the SNI server_name from a TLS ClientHello
// record without terminating TLS. It returns the host name and true on
// success. The input should be the first bytes of the client's TCP stream.
// It parses defensively and never panics on malformed input.
func sniffClientHelloSNI(data []byte) (string, bool) {
	// TLS record header: type(1) version(2) length(2)
	if len(data) < 5 {
		return "", false
	}
	if data[0] != 0x16 { // handshake
		return "", false
	}
	recLen := int(binary.BigEndian.Uint16(data[3:5]))
	buf := data[5:]
	if recLen < len(buf) {
		buf = buf[:recLen]
	}
	// Handshake header: type(1) length(3)
	if len(buf) < 4 || buf[0] != 0x01 { // client_hello
		return "", false
	}
	hsLen := int(buf[1])<<16 | int(buf[2])<<8 | int(buf[3])
	body := buf[4:]
	if hsLen < len(body) {
		body = body[:hsLen]
	}
	// client_version(2) random(32)
	if len(body) < 34 {
		return "", false
	}
	p := body[34:]
	// session_id
	if len(p) < 1 {
		return "", false
	}
	sidLen := int(p[0])
	p = p[1:]
	if len(p) < sidLen {
		return "", false
	}
	p = p[sidLen:]
	// cipher_suites
	if len(p) < 2 {
		return "", false
	}
	csLen := int(binary.BigEndian.Uint16(p))
	p = p[2:]
	if len(p) < csLen {
		return "", false
	}
	p = p[csLen:]
	// compression_methods
	if len(p) < 1 {
		return "", false
	}
	cmLen := int(p[0])
	p = p[1:]
	if len(p) < cmLen {
		return "", false
	}
	p = p[cmLen:]
	// extensions
	if len(p) < 2 {
		return "", false
	}
	extTotal := int(binary.BigEndian.Uint16(p))
	p = p[2:]
	if extTotal < len(p) {
		p = p[:extTotal]
	}
	for len(p) >= 4 {
		extType := binary.BigEndian.Uint16(p)
		extLen := int(binary.BigEndian.Uint16(p[2:]))
		p = p[4:]
		if len(p) < extLen {
			return "", false
		}
		ext := p[:extLen]
		p = p[extLen:]
		if extType != 0x0000 { // server_name
			continue
		}
		// server_name_list length(2)
		if len(ext) < 2 {
			return "", false
		}
		listLen := int(binary.BigEndian.Uint16(ext))
		list := ext[2:]
		if listLen < len(list) {
			list = list[:listLen]
		}
		// entries: name_type(1) length(2) name
		for len(list) >= 3 {
			nameType := list[0]
			nameLen := int(binary.BigEndian.Uint16(list[1:]))
			list = list[3:]
			if len(list) < nameLen {
				return "", false
			}
			name := list[:nameLen]
			list = list[nameLen:]
			if nameType == 0x00 { // host_name
				return string(name), true
			}
		}
	}
	return "", false
}
