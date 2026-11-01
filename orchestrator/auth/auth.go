// Package auth identifies the caller (design §6). Cognito implements
// Authenticator in Phase 8; until then tests and `make dev` use Fixed.
package auth

import (
	"net/http"
	"slices"
)

// Access bits, as stored on chunks in the index (design §5).
const (
	Public  uint32 = 0x1
	Invitee uint32 = 0x2
)

// InviteeGroup is the Cognito group whose members are invitees.
const InviteeGroup = "invitee"

// Caller is who sent a request. The zero value is an anonymous visitor.
type Caller struct {
	ID     string // stable user id; "" for anonymous
	Groups []string
}

// IsInvitee reports whether the caller may see private documents and use
// invitee tools.
func (c Caller) IsInvitee() bool { return slices.Contains(c.Groups, InviteeGroup) }

// Mask is the access mask for retrieval: public chunks for everyone, plus
// invitee chunks for invitees.
func (c Caller) Mask() uint32 {
	if c.IsInvitee() {
		return Public | Invitee
	}
	return Public
}

// Authenticator turns a request into a Caller.
type Authenticator interface {
	Authenticate(r *http.Request) (Caller, error)
}

// Fixed always returns the same caller.
type Fixed Caller

func (f Fixed) Authenticate(*http.Request) (Caller, error) { return Caller(f), nil }
