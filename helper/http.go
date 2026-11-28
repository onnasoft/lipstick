package helper

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"time"
)

func GetResponseWriter(w http.ResponseWriter) http.ResponseWriter {
	metaValue := reflect.ValueOf(w).Elem()
	field := metaValue.FieldByName("ResponseWriter")
	if field.IsValid() && field.Interface() != nil {
		value := field.Interface()
		return value.(http.ResponseWriter)
	}
	return w
}

func GetHijack(w http.ResponseWriter) (net.Conn, *bufio.ReadWriter, error) {
	sw := GetResponseWriter(w)
	hijacker, ok := sw.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("%v", "Hijacking not supported")
	}

	return hijacker.Hijack()
}

func GetDomainName(conn net.Conn) (string, error) {
	var rawConn net.Conn = conn
	connWithBuffer, ok := conn.(*ConnWithBuffer)
	if ok {
		rawConn = connWithBuffer.Conn
	}

	tlsConn, ok := rawConn.(*tls.Conn)
	if !ok {
		return "localhost", nil
	}

	err := tlsConn.Handshake()
	if err != nil {
		return "", err
	}

	state := tlsConn.ConnectionState()
	domain := state.ServerName

	return domain, nil
}

func IsHTTPRequest(data string) bool {
	lines := strings.Split(data, "\n")
	if len(lines) == 0 {
		return false
	}

	requestLine := strings.TrimSpace(lines[0])
	parts := strings.Split(requestLine, " ")
	if len(parts) != 3 {
		return false
	}

	method := parts[0]
	validMethods := map[string]bool{
		"GET":     true,
		"POST":    true,
		"PUT":     true,
		"DELETE":  true,
		"PATCH":   true,
		"OPTIONS": true,
		"HEAD":    true,
	}
	if !validMethods[method] {
		return false
	}

	version := parts[2]
	return strings.HasPrefix(version, "HTTP/")
}

func ParseHTTPRequest(conn net.Conn) (*http.Request, error) {
	reader := bufio.NewReader(conn)
	request, err := http.ReadRequest(reader)
	if err != nil {
		return nil, fmt.Errorf("error parsing HTTP request: %w", err)
	}

	return request, nil
}

const (
	// maxHeaderBytes bounds a header block, so a client cannot grow memory
	// without limit by never ending its headers.
	maxHeaderBytes = 64 << 10

	// headerReadTimeout bounds how long a client may take to send its headers.
	headerReadTimeout = 10 * time.Second
)

// ErrHeadersTooLarge is returned when a request's headers pass maxHeaderBytes.
var ErrHeadersTooLarge = errors.New("headers exceed 64 KiB")

// ReadUntilHeadersEnd reads a header block up to and including the empty line
// that ends it. Both \r\n and \n line endings are accepted.
//
// It reads one byte at a time on purpose. A buffered reader would read past the
// end of the headers and throw away the start of the body. With this reader the
// body stays on the connection. The read deadline is cleared before returning,
// so the connection can be used afterwards without a stale deadline.
func ReadUntilHeadersEnd(conn net.Conn) ([]byte, error) {
	if err := conn.SetReadDeadline(time.Now().Add(headerReadTimeout)); err != nil {
		return nil, fmt.Errorf("error setting header deadline: %w", err)
	}
	defer conn.SetReadDeadline(time.Time{})

	result := make([]byte, 0, 512)
	one := make([]byte, 1)
	for {
		if _, err := io.ReadFull(conn, one); err != nil {
			return nil, fmt.Errorf("error reading headers: %w", err)
		}
		result = append(result, one[0])
		if len(result) > maxHeaderBytes {
			return nil, ErrHeadersTooLarge
		}
		// An empty line ends the headers: "\n\n", "\r\n\r\n", or a mix.
		if n := len(result); n >= 2 && (string(result[n-2:]) == "\n\n" || (n >= 3 && string(result[n-3:]) == "\n\r\n")) {
			return result, nil
		}
	}
}
