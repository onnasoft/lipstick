package helper

// Reading the server name a client asks for (SNI) from its ClientHello. The
// proxy uses it to choose a domain before any handshake is answered.

// TLSClientHelloServerName reads the SNI from the first TLS record of a connection,
// which must be a complete ClientHello. It checks every length and never panics.
//
// A TLS record is: type(1)=22, version(2), length(2). A handshake message is:
// type(1)=1 for ClientHello, length(3). Only a ClientHello in one record is read.
func TLSClientHelloServerName(record []byte) (string, bool) {
	const recordHeader = 5
	const handshakeHeader = 4
	if len(record) < recordHeader+handshakeHeader || record[0] != 22 {
		return "", false
	}
	recLen := int(record[3])<<8 | int(record[4])
	body := record[recordHeader:]
	if len(body) < recLen {
		return "", false
	}
	body = body[:recLen]
	if body[0] != 1 {
		return "", false
	}
	msgLen := int(body[1])<<16 | int(body[2])<<8 | int(body[3])
	msg := body[handshakeHeader:]
	if len(msg) < msgLen {
		return "", false
	}
	return clientHelloSNI(msg[:msgLen], false)
}

// DTLSClientHelloServerName reads the SNI from the first datagram of a DTLS
// handshake, which is the ClientHello. A ClientHello fragmented over several
// datagrams is refused.
//
// A DTLS record is: type(1)=22, version(2), epoch(2), sequence(6), length(2).
// A handshake message is: type(1)=1, length(3), message_seq(2), fragment_offset(3),
// fragment_length(3).
func DTLSClientHelloServerName(d []byte) (string, bool) {
	const recordHeader = 13
	const handshakeHeader = 12
	if len(d) < recordHeader+handshakeHeader || d[0] != 22 {
		return "", false
	}
	h := d[recordHeader:]
	if h[0] != 1 {
		return "", false
	}
	msgLen := int(h[1])<<16 | int(h[2])<<8 | int(h[3])
	fragOff := int(h[6])<<16 | int(h[7])<<8 | int(h[8])
	fragLen := int(h[9])<<16 | int(h[10])<<8 | int(h[11])
	if fragOff != 0 || fragLen != msgLen {
		return "", false
	}
	body := h[handshakeHeader:]
	if len(body) < msgLen {
		return "", false
	}
	return clientHelloSNI(body[:msgLen], true)
}

// clientHelloSNI reads the server name from a ClientHello body. DTLS has a cookie
// field that TLS does not.
func clientHelloSNI(body []byte, dtls bool) (string, bool) {
	r := &reader{b: body}
	r.skip(2 + 32) // client_version, random
	r.skipVec(1)   // session_id
	if dtls {
		r.skipVec(1) // cookie
	}
	r.skipVec(2) // cipher_suites
	r.skipVec(1) // compression_methods
	extLen, ok := r.u16()
	if !ok {
		return "", false
	}
	exts, ok := r.bytes(extLen)
	if !ok {
		return "", false
	}
	er := &reader{b: exts}
	for er.len() >= 4 {
		typ, _ := er.u16()
		n, _ := er.u16()
		data, ok := er.bytes(n)
		if !ok {
			return "", false
		}
		if typ != 0 { // server_name
			continue
		}
		// server_name_list: length(2), then entries of name_type(1), length(2), name.
		sn := &reader{b: data}
		listLen, ok := sn.u16()
		if !ok {
			return "", false
		}
		list, ok := sn.bytes(listLen)
		if !ok {
			return "", false
		}
		ln := &reader{b: list}
		for ln.len() >= 3 {
			nameType, _ := ln.u8()
			nameLen, _ := ln.u16()
			name, ok := ln.bytes(nameLen)
			if !ok {
				return "", false
			}
			if nameType == 0 && len(name) > 0 {
				return string(name), true
			}
		}
		return "", false
	}
	return "", false
}

// reader reads big-endian fields from a byte slice without panicking.
type reader struct {
	b []byte
}

func (r *reader) len() int { return len(r.b) }

func (r *reader) u8() (int, bool) {
	if len(r.b) < 1 {
		return 0, false
	}
	v := int(r.b[0])
	r.b = r.b[1:]
	return v, true
}

func (r *reader) u16() (int, bool) {
	if len(r.b) < 2 {
		return 0, false
	}
	v := int(r.b[0])<<8 | int(r.b[1])
	r.b = r.b[2:]
	return v, true
}

func (r *reader) bytes(n int) ([]byte, bool) {
	if n < 0 || len(r.b) < n {
		return nil, false
	}
	v := r.b[:n]
	r.b = r.b[n:]
	return v, true
}

func (r *reader) skip(n int) {
	if len(r.b) >= n {
		r.b = r.b[n:]
	} else {
		r.b = nil
	}
}

// skipVec skips a vector whose length has size bytes (1 or 2).
func (r *reader) skipVec(size int) {
	var n int
	if size == 1 {
		n, _ = r.u8()
	} else {
		n, _ = r.u16()
	}
	r.skip(n)
}
