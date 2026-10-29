package timesheet

// ReviewContext returns the existing endpoint/credential binding for an explicit
// CLI plan review. It grants no authority and is not human-presence evidence.
func (s *Session) ReviewContext() (string, error) {
	return approvalContextDigest(s.cfg, s.env)
}
