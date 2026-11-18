package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManifestIdentity(t *testing.T) {
	if name != "octoeverywhere" || release == "" {
		t.Fatal("manifest identity is invalid")
	}
}
func TestRoutesRequireHostPrincipal(t *testing.T) {
	app := &application{}
	for _, path := range []string{"/status", "/settings/gadget", "/settings/connect", "/printers/example"} {
		method := http.MethodPost
		if path == "/status" {
			method = http.MethodGet
		}
		if path == "/printers/example" {
			method = http.MethodPut
		}
		response := httptest.NewRecorder()
		app.routes().ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(`{}`)))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s status %d", path, response.Code)
		}
	}
}
func TestStrictJSON(t *testing.T) {
	for _, body := range []string{`{"unknown":true}`, `{} {}`, `{`} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		w := httptest.NewRecorder()
		var args struct{}
		if readBody(w, r, &args) {
			t.Fatalf("accepted %q", body)
		}
	}
}
