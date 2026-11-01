package auth

import "testing"

func TestMask(t *testing.T) {
	if got := (Caller{}).Mask(); got != Public {
		t.Errorf("anonymous mask = %#x, want %#x", got, Public)
	}
	if got := (Caller{ID: "u1", Groups: []string{"other"}}).Mask(); got != Public {
		t.Errorf("signed-in non-invitee mask = %#x, want %#x", got, Public)
	}
	if got := (Caller{ID: "u2", Groups: []string{InviteeGroup}}).Mask(); got != Public|Invitee {
		t.Errorf("invitee mask = %#x, want %#x", got, Public|Invitee)
	}
}
