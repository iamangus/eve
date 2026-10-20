package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSPAFallbackDoesNotHideMissingAPIsOrAssets(t *testing.T) {
	h := SPAHandler(fstest.MapFS{"index.html": {Data: []byte("<h1>Eve</h1>")}, "assets/test.js": {Data: []byte("console.log('eve')")}})
	for _, tc := range []struct {
		path string
		code int
	}{{"/chat", 200}, {"/api/missing", 404}, {"/assets/missing.js", 404}, {"/mcp/missing", 404}, {"/assets/test.js", 200}} {
		response := httptest.NewRecorder()
		h(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if response.Code != tc.code {
			t.Fatalf("%s returned %d", tc.path, response.Code)
		}
		if tc.path == "/assets/test.js" && !strings.Contains(response.Header().Get("Cache-Control"), "immutable") {
			t.Fatal("hashed assets not cacheable")
		}
	}
}
