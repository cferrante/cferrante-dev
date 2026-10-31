package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A failed request must come back as a report, not a Lambda error, so
// bench.sh counts it as a failed run with its reason.
func TestHandlerReportsRequestErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"message":"rate limit reached"}}`)
	}))
	defer srv.Close()
	client.BaseURL, client.APIKey = srv.URL, "test"

	rep, err := handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler returned an error: %v", err)
	}
	if rep.OK || len(rep.Problems) != 1 || !strings.Contains(rep.Problems[0], "HTTP 429") {
		t.Errorf("report = %+v, want OK false and the 429 as its problem", rep)
	}
	if rep.Model != model {
		t.Errorf("model = %q, want %q", rep.Model, model)
	}
}
