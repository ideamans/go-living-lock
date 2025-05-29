package livinglock

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquire_StaleLock(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Create mock dependencies
	mockFS := &mockFileSystem{files: make(map[string][]byte)}
	mockClock := &mockSystemClock{currentTime: time.Now()}
	mockPM := &mockProcessManager{pid: 1000}

	deps := &Dependencies{
		FileSystem:     mockFS,
		Clock:          mockClock,
		ProcessManager: mockPM,
	}

	// Create stale lock (2 hours old)
	staleLockInfo := LockInfo{
		ProcessID: 2000,
		Timestamp: mockClock.currentTime.Add(-2 * time.Hour),
	}
	data, _ := json.Marshal(staleLockInfo)
	mockFS.files[lockPath] = data

	// Configure stale timeout to 1 hour
	options := Options{
		StaleTimeout: time.Hour,
		Dependencies: deps,
	}

	// Should be able to acquire stale lock
	lock, err := Acquire(lockPath, options)
	if err != nil {
		t.Fatalf("Should be able to acquire stale lock: %v", err)
	}
	defer lock.Release()

	// Verify new lock has current PID
	newLockData := mockFS.files[lockPath]
	var newLockInfo LockInfo
	json.Unmarshal(newLockData, &newLockInfo)

	if newLockInfo.ProcessID != 1000 {
		t.Errorf("Expected new lock to have PID 1000, got %d", newLockInfo.ProcessID)
	}
}

func TestAcquire_StaleLock_ProcessExists(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Create mock dependencies where stale process exists
	mockFS := &mockFileSystem{files: make(map[string][]byte)}
	mockClock := &mockSystemClock{currentTime: time.Now()}
	mockPM := &mockProcessManager{
		pid:               1000,
		existingProcesses: map[int]bool{2000: true}, // Stale process exists
		killCalled:        make(map[int]bool),
	}

	deps := &Dependencies{
		FileSystem:     mockFS,
		Clock:          mockClock,
		ProcessManager: mockPM,
	}

	// Create stale lock from existing process
	staleLockInfo := LockInfo{
		ProcessID: 2000,
		Timestamp: mockClock.currentTime.Add(-2 * time.Hour),
	}
	data, _ := json.Marshal(staleLockInfo)
	mockFS.files[lockPath] = data

	options := Options{
		StaleTimeout: time.Hour,
		Dependencies: deps,
	}

	// Should acquire lock and kill stale process
	lock, err := Acquire(lockPath, options)
	if err != nil {
		t.Fatalf("Should be able to acquire stale lock after killing process: %v", err)
	}
	defer lock.Release()

	// Verify kill was called on stale process
	if !mockPM.killCalled[2000] {
		t.Error("Kill should have been called on stale process 2000")
	}
}

func TestAcquire_StaleLock_ProcessNotExists(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Create mock dependencies where stale process doesn't exist
	mockFS := &mockFileSystem{files: make(map[string][]byte)}
	mockClock := &mockSystemClock{currentTime: time.Now()}
	mockPM := &mockProcessManager{
		pid:               1000,
		existingProcesses: map[int]bool{}, // Stale process doesn't exist
		killCalled:        make(map[int]bool),
	}

	deps := &Dependencies{
		FileSystem:     mockFS,
		Clock:          mockClock,
		ProcessManager: mockPM,
	}

	// Create stale lock from non-existing process
	staleLockInfo := LockInfo{
		ProcessID: 2000,
		Timestamp: mockClock.currentTime.Add(-2 * time.Hour),
	}
	data, _ := json.Marshal(staleLockInfo)
	mockFS.files[lockPath] = data

	options := Options{
		StaleTimeout: time.Hour,
		Dependencies: deps,
	}

	// Should acquire lock without trying to kill non-existing process
	lock, err := Acquire(lockPath, options)
	if err != nil {
		t.Fatalf("Should be able to acquire stale lock from non-existing process: %v", err)
	}
	defer lock.Release()

	// Verify kill was NOT called (process doesn't exist)
	if mockPM.killCalled[2000] {
		t.Error("Kill should NOT have been called on non-existing process 2000")
	}
}

func TestStaleTimeout_Boundary(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	baseTime := time.Now()
	mockFS := &mockFileSystem{files: make(map[string][]byte)}
	mockClock := &mockSystemClock{currentTime: baseTime}
	mockPM := &mockProcessManager{pid: 1000}

	deps := &Dependencies{
		FileSystem:     mockFS,
		Clock:          mockClock,
		ProcessManager: mockPM,
	}

	staleTimeout := time.Hour

	// Test lock that is exactly at the boundary (should NOT be stale)
	lockInfo := LockInfo{
		ProcessID: 2000,
		Timestamp: baseTime.Add(-staleTimeout), // Exactly stale timeout ago
	}
	data, _ := json.Marshal(lockInfo)
	mockFS.files[lockPath] = data

	options := Options{
		StaleTimeout: staleTimeout,
		Dependencies: deps,
	}

	// Should NOT be able to acquire (not stale yet)
	_, err := Acquire(lockPath, options)
	if err != ErrLockBusy {
		t.Errorf("Expected ErrLockBusy for boundary case, got: %v", err)
	}

	// Test lock that is just over the boundary (should be stale)
	lockInfo.Timestamp = baseTime.Add(-staleTimeout - time.Millisecond)
	data, _ = json.Marshal(lockInfo)
	mockFS.files[lockPath] = data

	// Should be able to acquire (now stale)
	lock, err := Acquire(lockPath, options)
	if err != nil {
		t.Fatalf("Should be able to acquire stale lock: %v", err)
	}
	defer lock.Release()
}

func TestAcquire_StaleLock_KillError(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Create mock dependencies where kill fails
	mockFS := &mockFileSystem{files: make(map[string][]byte)}
	mockClock := &mockSystemClock{currentTime: time.Now()}
	mockPM := &mockProcessManager{
		pid:               1000,
		existingProcesses: map[int]bool{2000: true},
		killError:         map[int]error{2000: os.ErrPermission}, // Kill fails
	}

	deps := &Dependencies{
		FileSystem:     mockFS,
		Clock:          mockClock,
		ProcessManager: mockPM,
	}

	// Create stale lock
	staleLockInfo := LockInfo{
		ProcessID: 2000,
		Timestamp: mockClock.currentTime.Add(-2 * time.Hour),
	}
	data, _ := json.Marshal(staleLockInfo)
	mockFS.files[lockPath] = data

	options := Options{
		StaleTimeout: time.Hour,
		Dependencies: deps,
	}

	// Should fail to acquire due to kill error
	_, err := Acquire(lockPath, options)
	if err == nil {
		t.Error("Should fail to acquire lock when kill fails")
	}
	if err == ErrLockBusy {
		t.Error("Should not return ErrLockBusy when kill fails")
	}
}
