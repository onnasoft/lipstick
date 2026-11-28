package admin

import (
	"embed"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// The panel and the login form are served from these files. They are compiled into
// the binary, and the policy sent with them allows no inline script or style.
//
//go:embed web/panel.css web/panel.js web/login.js
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

const loginHTML = `<!DOCTYPE html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Lipstick · Entrar</title>
<link rel="stylesheet" href="/admin/panel.css">
</head>
<body>
<div class="login">
  <h1>Lipstick</h1>
  <form id="login-form" autocomplete="on">
    <label for="username">Usuario</label>
    <input id="username" name="username" autocomplete="username" required autofocus>
    <label for="password">Contraseña</label>
    <input id="password" name="password" type="password" autocomplete="current-password" required>
    <button type="submit">Entrar</button>
    <div id="login-error" class="error" role="alert"></div>
  </form>
</div>
<script src="/admin/login.js"></script>
</body>
</html>
`

// serveAsset answers with one embedded file. The content never changes between
// releases, so the browser may cache it for a while.
func serveAsset(name, contentType string) gin.HandlerFunc {
	data, err := webFiles.ReadFile("web/" + name)
	if err != nil {
		panic("embedded asset missing: " + name)
	}
	return func(c *gin.Context) {
		c.Header("Content-Security-Policy", pageCSP)
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Cache-Control", "max-age=3600")
		c.Data(http.StatusOK, contentType, data)
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
