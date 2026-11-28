package admin

// @title Lipstick Admin API
// @version 1.0
// @description Administration of a Lipstick server: domains, live state, request log, visitor users and API tokens, and access rules.
// @description
// @description The API is served over TLS. The example configuration binds it to 127.0.0.1, so only the machine itself can reach it. Reads of domains, statistics and the request log need no key, which is safe only while the port is not reachable from other machines. Every change needs the admin key.
// @description
// @description Use the Authorize button and send the admin key from admin_secret_key in server/config.yml. It is sent as it is, without a "Bearer" prefix.
// @description
// @description Example with curl. The certificate is self-signed, so use -k. Replace the key with admin_secret_key from server/config.yml:
// @description
// @description `curl -k -X POST 'https://127.0.0.1:5052/domains/localhost/visitor-users' -H 'Content-Type: application/json' -H 'Authorization: <admin_secret_key>' -d '{"username": "jtorres"}'`
// @description
// @description Without the Authorization header the answer is 401 Unauthorized.
// @BasePath /
// @schemes https
// @securityDefinitions.apikey AdminKey
// @in header
// @name Authorization
// @description Admin key, sent as it is (no "Bearer" prefix).
func swaggerInfo() {}
