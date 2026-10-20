package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStrictJSONAndCrossOriginProtection(t *testing.T) {
	for _, body := range []string{`{"name":"x"} {}`, `{"unknown":true}`} {
		var value struct {
			Name string `json:"name"`
		}
		if err := Decode(httptest.NewRequest("POST", "/api/test", strings.NewReader(body)), &value); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	h := Harden(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest("POST", "http://eve.test/api/test", strings.NewReader(`{}`))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-site mutation accepted: %d", response.Code)
	}
}
