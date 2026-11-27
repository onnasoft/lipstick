package helper

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// SNIListener is the proxy's listener. It reads the first TLS record of each new
// connection and takes the SNI from it. A route function may take the connection
// when it wants to forward the TLS as it is, without ending it. Every other
// connection is ended here with the server's certificate, and Accept returns it.
type SNIListener struct {
	raw       net.Listener
	tlsConfig *tls.Config
	ready     chan net.Conn
	done      chan struct{}
	once      sync.Once

	mu    sync.RWMutex
	route RouteFunc
}

// RouteFunc is asked about a TLS connection whose SNI names domain. prefix holds
// the bytes already read from conn, which the function must forward first. It
// returns true when it has taken the connection. It returns false to leave the
// connection to the server's TLS.
type RouteFunc func(domain string, conn net.Conn, prefix []byte) bool

const (
	recordReadTimeout = 10 * time.Second
	maxTLSRecordBody  = 16384 + 2048
)

// NewSNIListener starts accepting on raw. tlsConfig is used for the connections
// that are not taken by a route.
func NewSNIListener(raw net.Listener, tlsConfig *tls.Config) *SNIListener {
	l := &SNIListener{
		raw:       raw,
		tlsConfig: tlsConfig,
		ready:     make(chan net.Conn),
		done:      make(chan struct{}),
	}
	go l.run()
	return l
}

// SetRoute sets the function asked about passthrough connections. Until it is set,
// every connection is ended here.
func (l *SNIListener) SetRoute(fn RouteFunc) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.route = fn
}

func (l *SNIListener) getRoute() RouteFunc {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.route
}

// Accept returns a connection whose TLS is ended here.
func (l *SNIListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ready:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

// Close stops accepting.
func (l *SNIListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return l.raw.Close()
}

// Addr returns the listening address.
func (l *SNIListener) Addr() net.Addr { return l.raw.Addr() }

func (l *SNIListener) run() {
	for {
		c, err := l.raw.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go l.handle(c)
	}
}

func (l *SNIListener) handle(c net.Conn) {
	record, _ := readFirstRecord(c)
	if sni, ok := TLSClientHelloServerName(record); ok {
		domain := strings.ToLower(strings.TrimRight(sni, "."))
		if fn := l.getRoute(); fn != nil && fn(domain, c, record) {
			return
		}
	}
	tc := tls.Server(NewConnWithBuffer(c, record), l.tlsConfig)
	select {
	case l.ready <- tc:
	case <-l.done:
		c.Close()
	}
}

// readFirstRecord reads the first TLS record, or as much as there is of it. It
// reads the body only when the header says it is a handshake record of sane size,
// so a plain HTTP request or junk is not read as a record.
func readFirstRecord(c net.Conn) ([]byte, error) {
	c.SetReadDeadline(time.Now().Add(recordReadTimeout))
	defer c.SetReadDeadline(time.Time{})

	hdr := make([]byte, 5)
	n, err := io.ReadFull(c, hdr)
	buf := hdr[:n]
	if err != nil || buf[0] != 22 {
		return buf, err
	}
	length := int(buf[3])<<8 | int(buf[4])
	if length == 0 || length > maxTLSRecordBody {
		return buf, nil
	}
	body := make([]byte, length)
	m, err := io.ReadFull(c, body)
	return append(buf, body[:m]...), err
}
