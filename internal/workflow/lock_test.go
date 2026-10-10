package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// deadPID returns a PID that is confirmed not alive: it runs a trivial process to
// completion (reaping it) and uses that PID. After the wait the PID is gone from
// the process table, so processAlive reports false.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to spawn helper process for a dead PID: %v", err)
	}
	return cmd.Process.Pid
}

func writeRawLock(t *testing.T, root, date, content string) string {
	t.Helper()
	wfDir := filepath.Join(root, "workflow")
	if err := os.MkdirAll(wfDir, DirPermPrivate); err != nil {
		t.Fatalf("failed to create workflow dir: %v", err)
	}
	path := filepath.Join(wfDir, "."+date+".lock")
	if err := os.WriteFile(path, []byte(content), FilePermPrivate); err != nil {
		t.Fatalf("failed to write lock file: %v", err)
	}
	return path
}

func lockLine(pid int, host, token string) string {
	return fmt.Sprintf("pid=%d host=%s token=%s acquired_at=2026-10-08T09:00:00Z\n", pid, host, token)
}

// TestAcquireDayLock_StaleVsBusy covers the fail-safe staleness contract: only a
// parseable, same-host lock whose owner is confirmed dead is stale; every other
// shape (live owner, foreign host, empty, S1 legacy without host, unparseable) is
// busy and never stale.
func TestAcquireDayLock_StaleVsBusy(t *testing.T) {
	ctx := context.Background()
	date := "2026-10-08"
	thisHost, err := os.Hostname()
	if err != nil {
		t.Fatalf("hostname: %v", err)
	}

	cases := []struct {
		name    string
		payload string
		want    error
	}{
		{"live pid same host", lockLine(os.Getpid(), thisHost, "00112233445566aa"), ErrWriterBusy},
		{"dead pid same host", lockLine(deadPID(t), thisHost, "00112233445566aa"), ErrWriterLockStale},
		{"foreign host", lockLine(deadPID(t), "elsewhere.invalid", "00112233445566aa"), ErrWriterBusy},
		{"empty payload", "", ErrWriterBusy},
		{"s1 legacy no host", fmt.Sprintf("pid=%d acquired_at=2026-10-08T09:00:00Z\n", deadPID(t)), ErrWriterBusy},
		{"unparseable", "garbage without separators\n", ErrWriterBusy},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := NewJSONFileStore(root)
			if err != nil {
				t.Fatalf("store: %v", err)
			}
			writeRawLock(t, root, date, tc.payload)

			_, err = store.AcquireDayLock(ctx, date)
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
			if errors.Is(err, ErrWriterLockStale) && errors.Is(tc.want, ErrWriterBusy) {
				t.Fatalf("foreign/empty/unparseable lock must never be stale")
			}
		})
	}
}

// TestAcquireDayLock_ReleaseIsOwnershipChecked proves a release only removes the
// lock it created, and that a released lock can be re-acquired.
func TestAcquireDayLock_ReleaseIsOwnershipChecked(t *testing.T) {
	ctx := context.Background()
	date := "2026-10-08"
	root := t.TempDir()
	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	release, err := store.AcquireDayLock(ctx, date)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	lockPath := store.lockPath(date)
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("lock file should exist while held: %v", err)
	}
	if _, ok := parseLockPayload(data); !ok {
		t.Fatalf("on-disk lock payload must parse, got %q", string(data))
	}

	// A second acquire while held is busy.
	if _, err := store.AcquireDayLock(ctx, date); !errors.Is(err, ErrWriterBusy) {
		t.Fatalf("expected ErrWriterBusy while held, got %v", err)
	}

	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("second release must be idempotent, got %v", err)
	}
	if _, statErr := os.Stat(lockPath); !os.IsNotExist(statErr) {
		t.Fatalf("lock file should be removed after release")
	}

	release2, err := store.AcquireDayLock(ctx, date)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	_ = release2()
}

// TestBreakStaleLock_FailsClosedAndRemovesOnProof proves the manual, token-gated
// break refuses a wrong token, refuses when the owner is not confirmed dead
// (ownerDead=false or a live owner), and removes the lock only on a matching
// token plus a confirmed-dead owner.
func TestBreakStaleLock_FailsClosedAndRemovesOnProof(t *testing.T) {
	ctx := context.Background()
	date := "2026-10-08"
	thisHost, err := os.Hostname()
	if err != nil {
		t.Fatalf("hostname: %v", err)
	}
	const token = "00112233445566aa"

	newStaleStore := func(t *testing.T) (*JSONFileStore, string) {
		root := t.TempDir()
		store, err := NewJSONFileStore(root)
		if err != nil {
			t.Fatalf("store: %v", err)
		}
		path := writeRawLock(t, root, date, lockLine(deadPID(t), thisHost, token))
		return store, path
	}

	t.Run("wrong token refused", func(t *testing.T) {
		store, path := newStaleStore(t)
		if err := store.BreakStaleLock(ctx, date, "ffffffffffffffff", true); err == nil {
			t.Fatal("expected refusal on wrong token")
		}
		if _, statErr := os.Stat(path); statErr != nil {
			t.Errorf("lock must remain after a refused break: %v", statErr)
		}
	})

	t.Run("ownerDead=false refused", func(t *testing.T) {
		store, path := newStaleStore(t)
		if err := store.BreakStaleLock(ctx, date, token, false); err == nil {
			t.Fatal("expected refusal when caller does not assert a dead owner")
		}
		if _, statErr := os.Stat(path); statErr != nil {
			t.Errorf("lock must remain after a refused break: %v", statErr)
		}
	})

	t.Run("live owner refused", func(t *testing.T) {
		root := t.TempDir()
		store, err := NewJSONFileStore(root)
		if err != nil {
			t.Fatalf("store: %v", err)
		}
		path := writeRawLock(t, root, date, lockLine(os.Getpid(), thisHost, token))
		if err := store.BreakStaleLock(ctx, date, token, true); err == nil {
			t.Fatal("expected refusal for a live owner")
		}
		if _, statErr := os.Stat(path); statErr != nil {
			t.Errorf("lock must remain after a refused break: %v", statErr)
		}
	})

	t.Run("correct token and dead owner removes", func(t *testing.T) {
		store, path := newStaleStore(t)
		if err := store.BreakStaleLock(ctx, date, token, true); err != nil {
			t.Fatalf("expected break to succeed, got %v", err)
		}
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("lock must be removed after a successful break")
		}
		release, err := store.AcquireDayLock(ctx, date)
		if err != nil {
			t.Fatalf("acquire after break: %v", err)
		}
		_ = release()
	})
}

// TestDayLockInfo_SecretFree proves the diagnostic view reports owner metadata
// and never requires reading the token for display.
func TestDayLockInfo_SecretFree(t *testing.T) {
	root := t.TempDir()
	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	date := "2026-10-08"
	thisHost, _ := os.Hostname()

	info, err := store.DayLockInfo(date)
	if err != nil {
		t.Fatalf("DayLockInfo on absent lock: %v", err)
	}
	if info.Present {
		t.Fatalf("expected Present=false for an absent lock")
	}

	writeRawLock(t, root, date, lockLine(12345, thisHost, "abcdefabcdefabcd"))
	info, err = store.DayLockInfo(date)
	if err != nil {
		t.Fatalf("DayLockInfo: %v", err)
	}
	if !info.Present || info.PID != 12345 || info.Host != thisHost || info.AcquiredAt == "" {
		t.Fatalf("expected parsed owner metadata, got %+v", info)
	}
	if !strings.Contains(info.Path, date) {
		t.Errorf("expected lock path to reference the date, got %q", info.Path)
	}
}
