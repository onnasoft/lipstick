package admin

import "time"

// The response bodies of the admin API. Each handler writes one of these types, so
// the published reference cannot drift from what the server sends.

// errorResponse is the body of every failed request. The message says what went
// wrong. Each response in the reference lists the messages that can appear there.
type errorResponse struct {
	Error string `json:"error" example:"message describing what went wrong"`
}

// statusResponse confirms a change or reports health.
type statusResponse struct {
	Status string `json:"status" example:"ok"`
}

// visitorUserResponse confirms a user. Secret is present only when the server
// generated it (on create without a secret) and on rotation. It is shown once.
type visitorUserResponse struct {
	Status   string `json:"status" example:"ok"`
	Username string `json:"username" example:"jtorres"`
	Secret   string `json:"secret,omitempty" example:"V_adFY98sN6seUTCMqf38ZXYG2tbGt8ETJsIw3_7cBg"`
}

// visitorTokenCreated is returned once when a token is created. The token is not
// stored and cannot be read again. Its id is not in this response: list the tokens
// to get it.
type visitorTokenCreated struct {
	Name  string `json:"name" example:"reader"`
	Token string `json:"token" example:"q8Vx2nqT1sK9wEZl0cHjR3pYb7LmUo4aGdNfXiTr5Ys"`
	Note  string `json:"note" example:"shown once; store it now"`
}

// visitorTokenItem describes a token without its value.
type visitorTokenItem struct {
	ID           uint      `json:"id" example:"3"`
	Name         string    `json:"name" example:"reader"`
	Methods      string    `json:"methods" example:"GET,HEAD"`
	PathPrefixes string    `json:"pathPrefixes" example:"/api,/public"`
	CreatedAt    time.Time `json:"createdAt"`
	Revoked      bool      `json:"revoked" example:"false"`
}

// trafficResponse lists the bytes transferred per day for a domain. Field names of
// consumptions are the ones the database model uses.
type trafficResponse struct {
	Domain       string             `json:"domain" example:"robot.example.com"`
	From         string             `json:"from" example:"2026-11-01"`
	To           string             `json:"to" example:"2026-11-30"`
	Consumptions []dailyConsumption `json:"consumptions"`
}

// dailyConsumption is one day of traffic for a domain.
type dailyConsumption struct {
	ID        uint      `json:"ID" example:"12"`
	Domain    string    `json:"Domain" example:"robot.example.com"`
	Date      time.Time `json:"Date"`
	Month     string    `json:"Month" example:"2026-11"`
	BytesUsed int64     `json:"BytesUsed" example:"1048576"`
	CreatedAt time.Time `json:"CreatedAt"`
	UpdatedAt time.Time `json:"UpdatedAt"`
}

// domainCreateBody is the body of POST /domains.
type domainCreateBody struct {
	Name                     string   `json:"name" example:"robot.example.com"`
	ApiKey                   string   `json:"apiKey" example:"clave-del-cliente-de-al-menos-32-caracteres"`
	AllowMultipleConnections bool     `json:"allowMultipleConnections" example:"true"`
	Passthrough              bool     `json:"passthrough" example:"false"`
	AllowedCIDRs             []string `json:"allowedCidrs" example:"10.0.0.0/8,192.168.1.7"`
	BandwidthBytesPerSecond  int64    `json:"bandwidthBytesPerSecond" example:"0"`
	MonthlyQuotaBytes        int64    `json:"monthlyQuotaBytes" example:"0"`
}

// domainChangeBody is the body of PATCH /domains/{domainName}. Send only the fields to change.
type domainChangeBody struct {
	ApiKey                   *string   `json:"apiKey,omitempty"`
	AllowMultipleConnections *bool     `json:"allowMultipleConnections,omitempty"`
	Passthrough              *bool     `json:"passthrough,omitempty"`
	AllowedCIDRs             *[]string `json:"allowedCidrs,omitempty"`
	AuthLatched              *bool     `json:"authLatched,omitempty" example:"false"`
}

// webLoginResponse confirms a web login. The token is not in it: it is in the cookie.
type webLoginResponse struct {
	Username  string    `json:"username" example:"jtorres"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// healthResponse reports the server and each dependency it checks. A dependency is
// "ok" or "down"; the reason is only in the server's log.
type healthResponse struct {
	Status string            `json:"status" example:"ok"`
	Checks map[string]string `json:"checks"`
}

// readyResponse answers /ready. Reasons names the conditions not met; it is empty when ready.
type readyResponse struct {
	Status  string   `json:"status" example:"ready"`
	Reasons []string `json:"reasons" example:"draining"`
}

// domainCreated answers POST /domains. ApiKey is present only when the server made the key:
// it is shown once, and the server never returns it again.
type domainCreated struct {
	Status string `json:"status" example:"ok"`
	ApiKey string `json:"apiKey,omitempty" example:"3f9c0e5a7b1d4e2f8a6c9b0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f"`
}
