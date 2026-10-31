package egress

import (
	"encoding/binary"
	"errors"
	"io"
	"net/netip"
	"strings"
)

// Inspect a bounded ClientHello before opening the upstream. This prevents a
// CONNECT to an allowed CDN address carrying another tenant's TLS SNI. ECH is
// rejected because the intended inner hostname cannot be checked here.
func readClientHello(r io.Reader, host string) ([]byte, error) {
	var wire, handshake []byte
	for len(wire) < 65536 {
		header := make([]byte, 5)
		if _, err := io.ReadFull(r, header); err != nil {
			return nil, err
		}
		n := int(binary.BigEndian.Uint16(header[3:]))
		if header[0] != 22 || header[1] != 3 || n == 0 || n > 16384 || len(wire)+5+n > 65536 {
			return nil, errors.New("invalid TLS ClientHello")
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, err
		}
		wire = append(wire, header...)
		wire = append(wire, body...)
		handshake = append(handshake, body...)
		if len(handshake) < 4 {
			continue
		}
		want := int(handshake[1])<<16 | int(handshake[2])<<8 | int(handshake[3])
		if handshake[0] != 1 || want > 65532 {
			return nil, errors.New("invalid TLS ClientHello")
		}
		if len(handshake) < want+4 {
			continue
		}
		if err := checkHello(handshake[4:4+want], host); err != nil {
			return nil, err
		}
		return wire, nil
	}
	return nil, errors.New("TLS ClientHello exceeds limit")
}

func checkHello(b []byte, host string) error {
	deny := errors.New("TLS hostname not allowed")
	if len(b) < 35 {
		return deny
	}
	b = b[34:]
	n := int(b[0])
	b = b[1:]
	if len(b) < n+2 {
		return deny
	}
	b = b[n:]
	n = int(binary.BigEndian.Uint16(b))
	b = b[2:]
	if n%2 != 0 || len(b) < n+1 {
		return deny
	}
	b = b[n:]
	n = int(b[0])
	b = b[1:]
	if len(b) < n+2 {
		return deny
	}
	b = b[n:]
	n = int(binary.BigEndian.Uint16(b))
	b = b[2:]
	if len(b) != n {
		return deny
	}
	sni := ""
	seenSNI := false
	for len(b) > 0 {
		if len(b) < 4 {
			return deny
		}
		kind := binary.BigEndian.Uint16(b)
		n = int(binary.BigEndian.Uint16(b[2:]))
		b = b[4:]
		if len(b) < n {
			return deny
		}
		ext := b[:n]
		b = b[n:]
		if kind == 0xfe0d {
			return errors.New("encrypted TLS ClientHello not supported by exact-origin proxy")
		}
		if kind != 0 {
			continue
		}
		if seenSNI {
			return deny
		}
		seenSNI = true
		if len(ext) < 2 || int(binary.BigEndian.Uint16(ext)) != len(ext)-2 {
			return deny
		}
		ext = ext[2:]
		for len(ext) > 0 {
			if len(ext) < 3 {
				return deny
			}
			typ := ext[0]
			n = int(binary.BigEndian.Uint16(ext[1:]))
			ext = ext[3:]
			if len(ext) < n {
				return deny
			}
			if typ == 0 {
				if sni != "" {
					return deny
				}
				sni = string(ext[:n])
			}
			ext = ext[n:]
		}
	}
	if _, err := netip.ParseAddr(host); err == nil {
		if sni != "" && sni != host {
			return deny
		}
		return nil
	}
	if strings.ToLower(sni) != host || sni == "" {
		return deny
	}
	return nil
}
