package admin

import (
	"embed"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// The panel is served from these files, compiled into the binary. The stylesheet is
// built from frontend/app.css with Tailwind (npm run build). The page policy allows
// no inline script or style, so every page is a file of its own.
//
//go:embed web
var webFiles embed.FS

const (
	sessionCookie = "lipstick_session"
	// csrfHeader must be sent with every change made with the session cookie. A page
	// on another site cannot set a custom header without a preflight, and this server
	// never answers one, so the request is refused before it arrives.
	csrfHeader = "X-Lipstick-CSRF"

	// pageCSP is the policy of every page. Only files from this server may run.
	pageCSP = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; " +
		"img-src 'self' data:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
)

// pageFile reads an embedded page or asset. The path is always under web/.
func pageFile(name string) []byte {
	data, err := webFiles.ReadFile("web/" + name)
	if err != nil {
		panic("embedded file missing: " + name)
	}
	return data
}

// contentTypeOf returns the media type of an asset by its extension.
func contentTypeOf(name string) string {
	switch path.Ext(name) {
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	}
	return "application/octet-stream"
}

// serveStatic answers with one file from web/<dir>/. Only names that exist in the
// embedded tree are served; anything else is a 404, and nothing outside web/ can be read.
func serveStatic(dir string) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := strings.TrimPrefix(c.Param("file"), "/")
		if name == "" || strings.Contains(name, "/") {
			c.Status(http.StatusNotFound)
			return
		}
		data, err := webFiles.ReadFile("web/" + dir + "/" + name)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		c.Header("Content-Security-Policy", pageCSP)
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Cache-Control", "max-age=3600")
		c.Data(http.StatusOK, contentTypeOf(name), data)
	}
}

// setSessionCookie stores the session token where scripts cannot read it: HttpOnly,
// Secure (the admin port is TLS only), and SameSite=Strict so other sites never send it.
func setSessionCookie(c *gin.Context, token string, expires time.Time) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}
