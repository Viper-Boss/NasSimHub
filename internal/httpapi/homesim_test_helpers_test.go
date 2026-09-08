package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
)

func newHomeSIMJSONRequest(method, target string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, target, body)
	r.Header.Set("Content-Type", "application/json")
	return r
}
