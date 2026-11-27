package manager

import "testing"

// clientHello builds a DTLS ClientHello datagram with an SNI extension for host.
// An empty host builds one without SNI.
func clientHello(host string) []byte {
	var exts []byte
	if host != "" {
		name := []byte(host)
		entry := append([]byte{0, byte(len(name) >> 8), byte(len(name))}, name...) // name_type 0
		list := append([]byte{byte(len(entry) >> 8), byte(len(entry))}, entry...)
		exts = append(exts, 0, 0, byte(len(list)>>8), byte(len(list))) // type 0, length
		exts = append(exts, list...)
	}
	body := []byte{0xfe, 0xfd}               // client_version DTLS 1.2
	body = append(body, make([]byte, 32)...) // random
	body = append(body, 0)                   // session_id length 0
	body = append(body, 0)                   // cookie length 0
	body = append(body, 0, 2, 0xc0, 0x2b)    // cipher_suites: one suite
	body = append(body, 1, 0)                // compression_methods: null
	body = append(body, byte(len(exts)>>8), byte(len(exts)))
	body = append(body, exts...)

	n := len(body)
	hs := []byte{1, byte(n >> 16), byte(n >> 8), byte(n), 0, 0, 0, 0, 0, byte(n >> 16), byte(n >> 8), byte(n)}
	hs = append(hs, body...)
	rec := []byte{22, 0xfe, 0xfd, 0, 0, 0, 0, 0, 0, 0, 0, byte(len(hs) >> 8), byte(len(hs))}
	return append(rec, hs...)
}

func TestDTLSServerNameIsRead(t *testing.T) {
	name, ok := dtlsServerName(clientHello("localhost"))
	if !ok || name != "localhost" {
		t.Fatalf("got %q, %v; want localhost", name, ok)
	}
	name, ok = dtlsServerName(clientHello("robot-42.example.com"))
	if !ok || name != "robot-42.example.com" {
		t.Fatalf("got %q, %v", name, ok)
	}
}

// A ClientHello without SNI, a datagram that is not a handshake, and truncated
// datagrams at every length are refused without a panic.
func TestDTLSServerNameRefusesWhatItCannotRead(t *testing.T) {
	if _, ok := dtlsServerName(clientHello("")); ok {
		t.Fatal("ClientHello without SNI accepted")
	}
	notHandshake := clientHello("localhost")
	notHandshake[0] = 23 // application data
	if _, ok := dtlsServerName(notHandshake); ok {
		t.Fatal("application data accepted as a ClientHello")
	}
	full := clientHello("localhost")
	for i := 0; i < len(full); i++ {
		if _, ok := dtlsServerName(full[:i]); ok {
			t.Fatalf("truncated datagram of %d bytes accepted", i)
		}
	}
}
