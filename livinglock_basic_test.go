package livinglock

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquire_NewLock(t *testing.T) {
	// Create temporary file path
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Acquire lock for the first time
	lock, err := Acquire(lockPath, Options{})
	if err != nil {
		t.Fatalf("Failed to acquire new lock: %v", err)
	}
	defer lock.Release()

	// Verify lock file exists
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		t.Error("Lock file was not created")
	}

	// Verify lock is not released
	if lock.released {
		t.Error("Lock should not be marked as released")
	}
}

func TestAcquire_SamePID(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Acquire lock first time
	lock1, err := Acquire(lockPath, Options{})
	if err != nil {
		t.Fatalf("Failed to acquire lock first time: %v", err)
	}
	defer lock1.Release()

	// Same process should be able to re-acquire its own lock
	lock2, err := Acquire(lockPath, Options{})
	if err != nil {
		t.Fatalf("Failed to re-acquire lock by same process: %v", err)
	}
	defer lock2.Release()

	// Both locks should be valid
	if lock1.released {
		t.Error("First lock should not be marked as released")
	}
	if lock2.released {
		t.Error("Second lock should not be marked as released")
	}
}

func TestAcquire_DifferentPID_Active(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Create mock dependencies to simulate different PID
	mockPM1 := &mockProcessManager{pid: 1000}
	mockPM2 := &mockProcessManager{pid: 2000}
	mockFS := &mockFileSystem{files: make(map[string][]byte)}
	mockClock := &mockSystemClock{currentTime: time.Now()}

	deps1 := &Dependencies{
		FileSystem:     mockFS,
		Clock:          mockClock,
		ProcessManager: mockPM1,
	}
	deps2 := &Dependencies{
		FileSystem:     mockFS,
		Clock:          mockClock,
		ProcessManager: mockPM2,
	}

	// First process acquires lock
	lock1, err := Acquire(lockPath, Options{Dependencies: deps1})
	if err != nil {
		t.Fatalf("Failed to acquire lock with first PID: %v", err)
	}
	defer lock1.Release()

	// Second process should fail to acquire lock
	_, err = Acquire(lockPath, Options{Dependencies: deps2})
	if err == nil {
		t.Error("Second process should not be able to acquire active lock")
	}
	if !errors.Is(err, ErrLockBusy) {
		t.Errorf("Expected ErrLockBusy, got: %v", err)
	}
}

func TestRelease(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Acquire lock
	lock, err := Acquire(lockPath, Options{})
	if err != nil {
		t.Fatalf("Failed to acquire lock: %v", err)
	}

	// Verify lock file exists
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		t.Error("Lock file should exist before release")
	}

	// Release lock
	err = lock.Release()
	if err != nil {
		t.Fatalf("Failed to release lock: %v", err)
	}

	// Verify lock file is removed
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("Lock file should be removed after release")
	}

	// Verify lock is marked as released
	if !lock.released {
		t.Error("Lock should be marked as released")
	}
}

func TestRelease_Multiple(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Acquire lock
	lock, err := Acquire(lockPath, Options{})
	if err != nil {
		t.Fatalf("Failed to acquire lock: %v", err)
	}

	// First release
	err = lock.Release()
	if err != nil {
		t.Fatalf("First release failed: %v", err)
	}

	// Second release should not error
	err = lock.Release()
	if err != nil {
		t.Fatalf("Second release should not error: %v", err)
	}

	// Third release should also not error
	err = lock.Release()
	if err != nil {
		t.Fatalf("Third release should not error: %v", err)
	}
}

func TestHeartBeat_Basic(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Acquire lock
	lock, err := Acquire(lockPath, Options{
		HeartBeatMinimalInterval: 0, // Always update
	})
	if err != nil {
		t.Fatalf("Failed to acquire lock: %v", err)
	}
	defer lock.Release()

	// Call heartbeat
	err = lock.HeartBeat()
	if err != nil {
		t.Fatalf("HeartBeat call failed: %v", err)
	}
}

func TestHeartBeat_AfterRelease(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Acquire lock
	lock, err := Acquire(lockPath, Options{})
	if err != nil {
		t.Fatalf("Failed to acquire lock: %v", err)
	}

	// Release lock
	err = lock.Release()
	if err != nil {
		t.Fatalf("Failed to release lock: %v", err)
	}

	// HeartBeat after release should be silently ignored
	err = lock.HeartBeat()
	if err != nil {
		t.Error("HeartBeat after release should not return error")
	}
}

// Mock implementations for testing

type mockFileSystem struct {
	files map[string][]byte
}

func (fs *mockFileSystem) ReadLockFile(filePath string) (LockInfo, error) {
	data, exists := fs.files[filePath]
	if !exists {
		return LockInfo{}, os.ErrNotExist
	}

	var lockInfo LockInfo
	if err := json.Unmarshal(data, &lockInfo); err != nil {
		return LockInfo{}, os.ErrNotExist
	}

	return lockInfo, nil
}

func (fs *mockFileSystem) WriteLockFile(filePath string, lockInfo LockInfo) error {
	data, err := json.Marshal(lockInfo)
	if err != nil {
		return err
	}
	fs.files[filePath] = data
	return nil
}

func (fs *mockFileSystem) RemoveLockFile(filePath string) error {
	delete(fs.files, filePath)
	return nil
}

type mockSystemClock struct {
	currentTime time.Time
}

func (c *mockSystemClock) Now() time.Time {
	return c.currentTime
}

type mockProcessManager struct {
	pid               int
	existingProcesses map[int]bool
	killCalled        map[int]bool
	killError         map[int]error
}

func (pm *mockProcessManager) GetPID() int {
	return pm.pid
}

func (pm *mockProcessManager) Exists(pid int) bool {
	if pm.existingProcesses == nil {
		return true // For basic tests, assume processes exist
	}
	return pm.existingProcesses[pid]
}

func (pm *mockProcessManager) Kill(pid int) error {
	if pm.killCalled == nil {
		pm.killCalled = make(map[int]bool)
	}
	pm.killCalled[pid] = true

	if pm.killError != nil {
		if err, exists := pm.killError[pid]; exists {
			return err
		}
	}
	return nil // For basic tests, killing always succeeds
}
