package safeio

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dh-kam/claude-creds-share/internal/common"
)

func TestAtomicWrite_Happy(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "f.json")

	if err := AtomicWrite(dst, []byte(`{"k":"v"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != `{"k":"v"}` {
		t.Fatalf("bad read: %s err=%v", got, err)
	}
	st, _ := os.Stat(dst)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v", st.Mode().Perm())
	}
	// No stray tmp files.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "f.json" {
			t.Fatalf("stray file left: %s", e.Name())
		}
	}
}

func TestAtomicWrite_MissingParent(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "nope", "f.json")
	err := AtomicWrite(dst, []byte("x"), 0o600)
	if err == nil {
		t.Fatalf("expected error for missing parent")
	}
	if !errors.Is(err, common.ErrApplyFailed) {
		t.Fatalf("want ErrApplyFailed, got %v", err)
	}
	// No tmp files under existing parent.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		t.Fatalf("unexpected entry: %s", e.Name())
	}
}

func TestLockFile_Sequential(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "f")

	unlock, err := LockFile(dst, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if err := unlock(); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	// Second should succeed immediately.
	unlock2, err := LockFile(dst, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("second lock: %v", err)
	}
	_ = unlock2()
}

func TestLockFile_ConcurrentTimeout(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "f")

	unlock, err := LockFile(dst, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	defer unlock()

	var wg sync.WaitGroup
	wg.Add(1)
	var gotErr error
	go func() {
		defer wg.Done()
		_, gotErr = LockFile(dst, 80*time.Millisecond)
	}()
	wg.Wait()
	if !errors.Is(gotErr, common.ErrLockTimeout) {
		t.Fatalf("want ErrLockTimeout, got %v", gotErr)
	}
}

func TestZeroize(t *testing.T) {
	Zeroize(nil) // no-op
	b := []byte("secret")
	Zeroize(b)
	for i, v := range b {
		if v != 0 {
			t.Fatalf("byte %d not zero: %v", i, v)
		}
	}
}

func TestReadWithBackup_AndRollback(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "f.json")
	orig := []byte("ORIGINAL")
	if err := AtomicWrite(dst, orig, 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	data, backupPath, err := ReadWithBackup(dst)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if !bytes.Equal(data, orig) {
		t.Fatalf("data mismatch")
	}
	st, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("backup stat: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode=%v", st.Mode().Perm())
	}

	// Simulate a failed apply: overwrite dst, then rollback.
	if err := AtomicWrite(dst, []byte("CORRUPTED"), 0o600); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if err := Rollback(backupPath, dst); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	got, _ := os.ReadFile(dst)
	if !bytes.Equal(got, orig) {
		t.Fatalf("rollback did not restore: %q", got)
	}
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Fatalf("backup not consumed: %v", err)
	}
}
