package helper

import "strconv"

var BadGatewayBody = `<!DOCTYPE html>
<html>
<head>
    <title>502 Bad Gateway</title>
</head>
<body>
    <h1>Bad Gateway</h1>
    <p>The server encountered a temporary error and could not complete your request.</p>
</body>
</html>`

// BadGatewayResponse is a complete HTTP/1.1 502 response. HTTP requires CRLF
// line endings, and the connection is closed after it.
var BadGatewayResponse = "HTTP/1.1 502 Bad Gateway\r\n" +
	"Content-Type: text/html\r\n" +
	"Content-Length: " + strconv.Itoa(len(BadGatewayBody)) + "\r\n" +
	"Connection: close\r\n" +
	"\r\n" +
	BadGatewayBody

// GatewayTimeoutResponse is a complete HTTP/1.1 504 response, sent when the backend does
// not answer a visitor's request in time.
var GatewayTimeoutResponse = "HTTP/1.1 504 Gateway Timeout\r\n" +
	"Content-Length: 0\r\n" +
	"Connection: close\r\n" +
	"\r\n"

// BadRequestResponse is a complete HTTP/1.1 400 response, sent when the request cannot be
// parsed. The connection is closed after it.
var BadRequestResponse = "HTTP/1.1 400 Bad Request\r\n" +
	"Content-Length: 0\r\n" +
	"Connection: close\r\n" +
	"\r\n"

// RequestHeaderFieldsTooLargeResponse is a complete HTTP/1.1 431 response, sent when the
// headers of a request exceed the limit. The connection is closed after it.
var RequestHeaderFieldsTooLargeResponse = "HTTP/1.1 431 Request Header Fields Too Large\r\n" +
	"Content-Length: 0\r\n" +
	"Connection: close\r\n" +
	"\r\n"
