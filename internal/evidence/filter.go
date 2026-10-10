package evidence

// IsProposalInput reports whether e is eligible as proposal input. It excludes
// cancelled events and declined/unknown invitations; those items remain in the
// evidence record and are filtered only here, at proposal time. This is a PURE
// predicate over Status and Invitation, so the proposal layer can use it without
// depending on any collector/plugin package.
func IsProposalInput(e Evidence) bool {
	if e.Status == EventStatusCancelled {
		return false
	}
	switch e.Invitation {
	case InvitationDeclined, InvitationUnknown:
		return false
	}
	return true
}
