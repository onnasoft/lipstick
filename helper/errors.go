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
