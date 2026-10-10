package workflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Day writer lock protocol.
//
// The lock file at <workflowDir>/.<date>.lock is created with O_CREATE|O_EXCL
// (0600) and holds a single line:
//
//	pid=<int> host=<hostname> token=<hex16> acquired_at=<RFC3339 UTC>
//
// Staleness is determined conservatively from the recorded owner PID and host:
//
//   - The payload must parse, name a host equal to this host, and record a PID
//     that is confirmed NOT alive before the lock is considered stale.
//   - Everything else (alive owner, foreign host, empty, unparseable) is
//     ErrWriterBusy. This includes S1-era lock files that have no host field:
//     they are treated as foreign/unknown and are never considered stale.
//
// WHY AUTO-BREAK IS UNSAFE (never break a lock automatically): a PID is only a
// reusable integer. Between the intent fsync and the remote write the true owner
// is alive; a recycled PID can make a dead owner look alive, and a lock written
// but not yet flushed can read as an empty payload. Auto-breaking either window
// lets a second writer acquire the same day and issue DUPLICATE, undeletable
// remote entries (Gradion entries are editable but not removable). Recovery is
// therefore manual and token-gated: BreakStaleLock re-verifies, under the lock
// file, that the on-disk token matches the operator-supplied token AND that the
// recorded owner is not alive on this host, and fails closed otherwise.
//
// RECOVERY LIMIT (intentional, fail-closed): both staleness classification and
// BreakStaleLock require the recorded host to equal this host AND the recorded
// PID to be confirmed not alive on it. Consequently a genuinely dead owner is
// NOT recoverable through this tool when either condition cannot be established:
//   - the lock was written on a different hostname (or the host was renamed /
//     the machine was cloned), so host mismatches and the lock looks foreign; or
//   - the PID was recycled by an unrelated live process, so the owner looks alive.
//
// When either occurs the day lock blocks submission and must be removed MANUALLY
// (delete <workflowDir>/.<date>.lock) after the operator confirms the true owner
// is gone. This is deliberate: the tool never risks a duplicate remote write to
// clear a lock it cannot prove is dead.

const lockTokenBytes = 8 // 8 bytes -> 16 hex characters

// LockInfo is a secret-free description of a day lock for diagnostics. Token is
// exposed for programmatic break flows and tests; it must never be rendered in
// operator-facing diagnostics.
type LockInfo struct {
	Present    bool
	Path       string
	PID        int
	Host       string
	Token      string
	AcquiredAt string
}

// lockPayload is the parsed content of a lock file.
type lockPayload struct {
	PID        int
	Host       string
	Token      string
	AcquiredAt string
}

// parseLockPayload parses a lock file. It returns ok=false when the payload is
// empty or missing any of the required identity fields (pid, host, token), which
// includes S1-era files that carry no host/token.
func parseLockPayload(data []byte) (lockPayload, bool) {
	var payload lockPayload
	hasPID := false
	for _, field := range strings.Fields(string(data)) {
		key, value, found := strings.Cut(field, "=")
		if !found {
			continue
		}
		switch key {
		case "pid":
			n, err := strconv.Atoi(value)
			if err != nil {
				return lockPayload{}, false
			}
			payload.PID = n
			hasPID = true
		case "host":
			payload.Host = value
		case "token":
			payload.Token = value
		case "acquired_at":
			payload.AcquiredAt = value
		}
	}
	if !hasPID || payload.Host == "" || payload.Token == "" {
		return lockPayload{}, false
	}
	return payload, true
}

// processAlive reports whether pid is a live process on this host. It fails
// safe: nil and EPERM mean the process exists (EPERM = exists but not ours), and
// any unexpected error is treated as alive so a lock is never misclassified as
// stale.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil:
		return true
	case errors.Is(err, syscall.EPERM):
		return true
	case errors.Is(err, syscall.ESRCH):
		return false
	default:
		return true
	}
}

// newLockToken returns a fresh random 16-hex-character lock token.
func newLockToken() (string, error) {
	buf := make([]byte, lockTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate workflow day lock token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func lockPayloadString(pid int, host, token string, at time.Time) string {
	return fmt.Sprintf("pid=%d host=%s token=%s acquired_at=%s\n",
		pid, host, token, at.UTC().Format(time.RFC3339))
}

// AcquireDayLock takes the exclusive per-day writer lock. It returns ErrWriterBusy
// when the lock is held by a live owner, a foreign host, or an empty/unparseable
// payload (fail safe). It returns ErrWriterLockStale only when the payload
// parses, names this host, and the recorded owner is confirmed not alive. Stale
// locks are never auto-broken. The returned release closure removes the lock and
// is idempotent; it only removes the lock if its own token still matches, so a
// release can never delete a lock acquired by another owner.
func (s *JSONFileStore) AcquireDayLock(ctx context.Context, date string) (func() error, error) {
	Assert(ctx != nil, AssertWorkflowContextNil)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateDate(date); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(s.workflowDir, DirPermPrivate); err != nil {
		return nil, fmt.Errorf("failed to ensure workflow directory exists: %w", err)
	}

	host, hostErr := os.Hostname()
	if hostErr != nil {
		// Fail safe: without a host identity this process cannot prove that a
		// pre-existing lock is foreign, so it must never acquire.
		return nil, ErrWriterBusy
	}
	token, tokenErr := newLockToken()
	if tokenErr != nil {
		return nil, tokenErr
	}

	lockPath := s.lockPath(date)
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, FilePermPrivate)
	if err != nil {
		if os.IsExist(err) {
			return nil, s.classifyExistingLock(lockPath)
		}
		return nil, fmt.Errorf("failed to acquire workflow day lock: %w", err)
	}

	if _, err := file.WriteString(lockPayloadString(os.Getpid(), host, token, time.Now().UTC())); err != nil {
		_ = file.Close()
		_ = os.Remove(lockPath)
		return nil, fmt.Errorf("failed to write workflow day lock: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(lockPath)
		return nil, fmt.Errorf("failed to close workflow day lock: %w", err)
	}

	var once sync.Once
	var releaseErr error
	release := func() error {
		once.Do(func() {
			releaseErr = s.removeLockIfOwned(lockPath, token)
		})
		return releaseErr
	}
	return release, nil
}

// classifyExistingLock inspects a pre-existing lock and returns the correct typed
// error. Stale is returned ONLY for a parseable, same-host lock whose owner is
// confirmed not alive.
func (s *JSONFileStore) classifyExistingLock(lockPath string) error {
	data, readErr := os.ReadFile(lockPath)
	if readErr != nil {
		return ErrWriterBusy
	}
	payload, ok := parseLockPayload(data)
	if !ok {
		return ErrWriterBusy
	}
	host, hostErr := os.Hostname()
	if hostErr != nil || payload.Host != host {
		return ErrWriterBusy
	}
	if processAlive(payload.PID) {
		return ErrWriterBusy
	}
	return ErrWriterLockStale
}

// removeLockIfOwned removes the lock only when its token still matches ours, so a
// release never deletes a lock that a different owner has since acquired.
func (s *JSONFileStore) removeLockIfOwned(lockPath, token string) error {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	payload, ok := parseLockPayload(data)
	if !ok || payload.Token != token {
		return nil
	}
	if err := os.Remove(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// DayLockInfo returns a secret-free description of the day lock without touching
// it. An absent lock returns Present=false. The caller must not render Token in
// diagnostics.
func (s *JSONFileStore) DayLockInfo(date string) (LockInfo, error) {
	if err := ValidateDate(date); err != nil {
		return LockInfo{}, err
	}
	info := LockInfo{Path: s.lockPath(date)}
	data, err := os.ReadFile(info.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return info, nil
		}
		return info, err
	}
	info.Present = true
	if payload, ok := parseLockPayload(data); ok {
		info.PID = payload.PID
		info.Host = payload.Host
		info.Token = payload.Token
		info.AcquiredAt = payload.AcquiredAt
	}
	return info, nil
}

// BreakStaleLock manually clears a stale day lock. It fails closed unless the
// caller supplies a non-empty expected token, asserts the owner is dead, and the
// on-disk lock re-verifies under this call: the token matches, the recorded host
// is this host, and the recorded owner is confirmed not alive. A missing lock is
// a no-op success. It never auto-breaks; see the file header for why.
func (s *JSONFileStore) BreakStaleLock(ctx context.Context, date, expectedToken string, ownerDead bool) error {
	Assert(ctx != nil, AssertWorkflowContextNil)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateDate(date); err != nil {
		return err
	}
	expectedToken = strings.TrimSpace(expectedToken)
	if expectedToken == "" || !ownerDead {
		return ErrWriterLockStale
	}

	lockPath := s.lockPath(date)
	data, err := os.ReadFile(lockPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("failed to read workflow day lock: %w", err)
	}

	payload, ok := parseLockPayload(data)
	if !ok {
		return ErrWriterBusy
	}
	if payload.Token != expectedToken {
		return ErrWriterLockStale
	}
	host, hostErr := os.Hostname()
	if hostErr != nil || payload.Host != host {
		return ErrWriterBusy
	}
	if processAlive(payload.PID) {
		return ErrWriterBusy
	}
	if err := os.Remove(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to remove stale workflow day lock: %w", err)
	}
	return nil
}
