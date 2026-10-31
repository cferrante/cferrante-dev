package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

func TestHandlerStreamsSSE(t *testing.T) {
	count, interval = 3, 10*time.Millisecond // keep the test fast

	res, err := handler(context.Background(), &events.LambdaFunctionURLRequest{})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.StatusCode != 200 {
		t.Errorf("status = %d, want 200", res.StatusCode)
	}
	if ct := res.Headers["Content-Type"]; ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if got := strings.Count(string(body), "event: tick\n"); got != 3 {
		t.Errorf("got %d events, want 3:\n%s", got, body)
	}
}
