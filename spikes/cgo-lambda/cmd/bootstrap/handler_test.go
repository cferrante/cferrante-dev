package main

import (
	"encoding/json"
	"testing"
)

func TestHandle(t *testing.T) {
	out, err := handle([]byte(`{"numbers":[1,2,3,4]}`))
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	var got response
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, out)
	}
	if got.Sum != 10 {
		t.Errorf("sum = %d, want 10", got.Sum)
	}
	if got.Libc == "" {
		t.Error("libc version is empty")
	}
}

func TestHandleRejectsBadEvent(t *testing.T) {
	if _, err := handle([]byte(`{"numbers":"oops"}`)); err == nil {
		t.Error("want an error for a malformed event")
	}
}
