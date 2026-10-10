package evidence

import "testing"

// TestIsProposalInput pins the pure proposal-input predicate.
func TestIsProposalInput(t *testing.T) {
	cases := []struct {
		name string
		e    Evidence
		want bool
	}{
		{"confirmed accepted", Evidence{Status: EventStatusConfirmed, Invitation: InvitationAccepted}, true},
		{"confirmed none", Evidence{Status: EventStatusConfirmed, Invitation: InvitationNone}, true},
		{"tentative kept", Evidence{Invitation: InvitationTentative}, true},
		{"needs action kept", Evidence{Invitation: InvitationNeedsAction}, true},
		{"cancelled excluded", Evidence{Status: EventStatusCancelled}, false},
		{"declined excluded", Evidence{Invitation: InvitationDeclined}, false},
		{"unknown excluded", Evidence{Invitation: InvitationUnknown}, false},
	}
	for _, tc := range cases {
		if got := IsProposalInput(tc.e); got != tc.want {
			t.Errorf("%s: IsProposalInput = %v, want %v", tc.name, got, tc.want)
		}
	}
}
