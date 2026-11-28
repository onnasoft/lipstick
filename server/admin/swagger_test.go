package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The published reference must describe the real API: the key scheme, the
// protected routes, and the public ones.
func TestSwaggerDocumentDescribesTheAPI(t *testing.T) {
	a := newTestAdmin("secret")
	rec := do(a, http.MethodGet, "/swagger/doc.json", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("doc.json status %d", rec.Code)
	}
	var spec struct {
		SecurityDefinitions map[string]struct {
			Type, Name, In string
		} `json:"securityDefinitions"`
		Paths map[string]map[string]struct {
			Security []map[string][]string `json:"security"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	key, ok := spec.SecurityDefinitions["AdminKey"]
	if !ok || key.Type != "apiKey" || key.In != "header" || key.Name != "Authorization" {
		t.Fatalf("AdminKey scheme missing or wrong: %+v", key)
	}

	protected := map[string][]string{
		"/domains":                                              {"post"},
		"/domains/{domainName}":                                 {"patch", "delete"},
		"/domains/{domainName}/visitor-users":                   {"post", "get"},
		"/domains/{domainName}/visitor-users/{username}":        {"delete"},
		"/domains/{domainName}/visitor-users/{username}/rotate": {"post"},
		"/domains/{domainName}/visitor-tokens":                  {"post", "get"},
		"/domains/{domainName}/visitor-tokens/{id}":             {"delete"},
	}
	for path, methods := range protected {
		for _, m := range methods {
			op, ok := spec.Paths[path][m]
			if !ok {
				t.Fatalf("%s %s is not documented", m, path)
			}
			if len(op.Security) == 0 {
				t.Errorf("%s %s changes data but declares no key", m, path)
			}
		}
	}
	for _, path := range []string{"/health", "/stats", "/domains", "/requests"} {
		if op, ok := spec.Paths[path]["get"]; ok && len(op.Security) != 0 {
			t.Errorf("GET %s is read-only but declares a key", path)
		}
	}
}

func TestSwaggerUIIsServed(t *testing.T) {
	a := newTestAdmin("secret")
	rec := do(a, http.MethodGet, "/swagger/index.html", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "swagger") {
		t.Fatalf("UI status %d", rec.Code)
	}
}

// Every response must name its type, so the reference shows the real bodies. A
// generic object would hide them.
func TestEveryResponseHasATypedSchema(t *testing.T) {
	a := newTestAdmin("secret")
	rec := do(a, http.MethodGet, "/swagger/doc.json", "")
	var spec struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Schema map[string]any `json:"schema"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	for path, ops := range spec.Paths {
		for method, op := range ops {
			for code, resp := range op.Responses {
				if code == "200" && path == "/" {
					continue // the HTML panel
				}
				if resp.Schema == nil {
					t.Errorf("%s %s %s has no schema", method, path, code)
					continue
				}
				if ap, ok := resp.Schema["additionalProperties"]; ok && resp.Schema["type"] == "object" {
					t.Errorf("%s %s %s is a generic object %v", method, path, code, ap)
				}
			}
		}
	}
	rotate := spec.Paths["/domains/{domainName}/visitor-users/{username}/rotate"]["post"]
	if _, ok := rotate.Responses["200"]; !ok {
		t.Fatal("rotate route is not documented")
	}
}
