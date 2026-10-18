package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAccessLogOmitsQueryAndCredentials(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := Chain(ok, RequestID, AccessLog(log))

	req := httptest.NewRequest(http.MethodGet, "/x?sig=topsecret", nil)
	req.SetBasicAuth("alice", "hunter2")
	h.ServeHTTP(httptest.NewRecorder(), req)

	out := logs.String()
	for _, leaked := range []string{"topsecret", "hunter2", "Authorization", "YWxpY2U6"} {
		if strings.Contains(out, leaked) {
			t.Errorf("log leaks %q: %s", leaked, out)
		}
	}
	if !strings.Contains(out, `"path":"/x"`) || !strings.Contains(out, `"status":204`) {
		t.Errorf("log is missing expected fields: %s", out)
	}
}

func TestAccessLogDefaultsToStatus200(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))

	silent := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	AccessLog(log)(silent).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if !strings.Contains(logs.String(), `"status":200`) {
		t.Errorf("log = %s, want status 200", logs.String())
	}
}
