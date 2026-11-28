package admin

// @title Lipstick Admin API
// @version 1.0
// @description Administration of a Lipstick server: domains, live state, request log, visitor users and API tokens, and access rules.
// @description
// @description The API is served over TLS. The example configuration binds it to 127.0.0.1, so only the machine itself can reach it. Everything except the health check and the login endpoints needs a session: the web panel gets it from its login form, API clients from /login.
// @description
// @description **Accounts.** Admin accounts are created on the server machine, from the console, with a password typed in the terminal: `lipstick-server admin create -username NAME`. There is no key to configure. Without an account no change is possible. Other console commands: `admin list`, `admin reset-password -username NAME`, `admin disable -username NAME`, `admin enable -username NAME`.
// @description
// @description **Sessions.** `POST /login` with the account's username and password returns a token. Send it on each request as `Authorization: Bearer <token>`. The web panel uses `POST /web/login` instead, which keeps the token in an HttpOnly cookie that scripts cannot read; changes made with that cookie must carry the header `X-Lipstick-CSRF: 1`. It expires after 12 hours; `POST /logout` ends it. Five wrong passwords lock the account for 15 minutes.
// @description
// @description **Example with curl.** The certificate is self-signed, so use -k:
// @description
// @description `curl -k -X POST 'https://127.0.0.1:5052/login' -H 'Content-Type: application/json' -d '{"username": "jtorres", "password": "<your password>"}'`
// @description
// @description Copy the token from the answer, then:
// @description
// @description `curl -k -X POST 'https://127.0.0.1:5052/domains/localhost/visitor-users' -H 'Content-Type: application/json' -H 'Authorization: Bearer <token>' -d '{"username": "jtorres"}'`
// @description
// @description Without a valid token the answer is 401 Unauthorized.
// @BasePath /
// @schemes https
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Session token from /login, sent as "Bearer <token>".
func swaggerInfo() {}
