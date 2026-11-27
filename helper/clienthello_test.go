package helper

import "testing"

// tlsClientHello builds a TLS ClientHello record with an SNI extension for host.
// An empty host builds one without SNI.
func tlsClientHello(host string) []byte {
	var exts []byte
	if host != "" {
		name := []byte(host)
		entry := append([]byte{0, byte(len(name) >> 8), byte(len(name))}, name...)
		list := append([]byte{byte(len(entry) >> 8), byte(len(entry))}, entry...)
		exts = append(exts, 0, 0, byte(len(list)>>8), byte(len(list)))
		exts = append(exts, list...)
	}
	body := []byte{0x03, 0x03}
	body = append(body, make([]byte, 32)...)
	body = append(body, 0)                // session_id
	body = append(body, 0, 2, 0xc0, 0x2b) // cipher_suites
	body = append(body, 1, 0)             // compression
	body = append(body, byte(len(exts)>>8), byte(len(exts)))
	body = append(body, exts...)

	n := len(body)
	hs := []byte{1, byte(n >> 16), byte(n >> 8), byte(n)}
	hs = append(hs, body...)
	rec := []byte{22, 0x03, 0x01, byte(len(hs) >> 8), byte(len(hs))}
	return append(rec, hs...)
}

func TestTLSServerNameIsRead(t *testing.T) {
	name, ok := TLSClientHelloServerName(tlsClientHello("robot-42.example.com"))
	if !ok || name != "robot-42.example.com" {
		t.Fatalf("got %q, %v", name, ok)
	}
}

// Without SNI, for a record that is not a handshake, and for every truncation, the
// name is refused and nothing panics.
func TestTLSServerNameRefusesWhatItCannotRead(t *testing.T) {
	if _, ok := TLSClientHelloServerName(tlsClientHello("")); ok {
		t.Fatal("ClientHello without SNI accepted")
	}
	notHandshake := tlsClientHello("localhost")
	notHandshake[0] = 23
	if _, ok := TLSClientHelloServerName(notHandshake); ok {
		t.Fatal("application data accepted")
	}
	full := tlsClientHello("localhost")
	for i := 0; i < len(full); i++ {
		if _, ok := TLSClientHelloServerName(full[:i]); ok {
			t.Fatalf("truncated record of %d bytes accepted", i)
		}
	}
}
