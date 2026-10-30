package canary

import "testing"

func TestDeliberateFailure(t *testing.T) {
	t.Fatal("deliberate failure: CI must go red")
}
