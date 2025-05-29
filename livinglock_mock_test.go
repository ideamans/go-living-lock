package livinglock

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMockFileSystem(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Create custom mock filesystem
	mockFS := &mockFileSystem{files: make(map[string][]byte)}
	mockClock := &mockSystemClock{currentTime: time.Now()}
	mockPM := &mockProcessManager{pid: 1000}

	deps := &Dependencies{
		FileSystem:     mockFS,
		Clock:          mockClock,
		ProcessManager: mockPM,
	}

	// Test that mock filesystem is used
	lock, err := Acquire(lockPath, Options{Dependencies: deps})
	if err != nil {
		t.Fatalf("Failed to acquire lock with mock filesystem: %v", err)
	}
	defer lock.Release()

	// Verify lock was written to mock filesystem, not real filesystem
	if _, exists := mockFS.files[lockPath]; !exists {
		t.Error("Lock should be written to mock filesystem")
	}

	// Verify no real file was created
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("Real file should not be created when using mock filesystem")
	}

	// Test reading from mock filesystem
	lockInfo, err := mockFS.ReadLockFile(lockPath)
	if err != nil {
		t.Fatalf("Failed to read from mock filesystem: %v", err)
	}

	if lockInfo.ProcessID != 1000 {
		t.Errorf("Expected PID 1000, got %d", lockInfo.ProcessID)
	}
}

func TestMockClock(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	fixedTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	mockFS := &mockFileSystem{files: make(map[string][]byte)}
	mockClock := &mockSystemClock{currentTime: fixedTime}
	mockPM := &mockProcessManager{pid: 1000}

	deps := &Dependencies{
		FileSystem:     mockFS,
		Clock:          mockClock,
		ProcessManager: mockPM,
	}

	// Acquire lock with fixed time
	lock, err := Acquire(lockPath, Options{Dependencies: deps})
	if err != nil {
		t.Fatalf("Failed to acquire lock: %v", err)
	}
	defer lock.Release()

	// Verify timestamp matches mock clock
	lockInfo, err := mockFS.ReadLockFile(lockPath)
	if err != nil {
		t.Fatalf("Failed to read lock info: %v", err)
	}

	if !lockInfo.Timestamp.Equal(fixedTime) {
		t.Errorf("Expected timestamp %v, got %v", fixedTime, lockInfo.Timestamp)
	}

	// Advance mock clock
	newTime := fixedTime.Add(time.Hour)
	mockClock.currentTime = newTime

	// HeartBeat should use new time
	err = lock.HeartBeat()
	if err != nil {
		t.Fatalf("HeartBeat failed: %v", err)
	}

	// Verify timestamp was updated to new time
	updatedLockInfo, err := mockFS.ReadLockFile(lockPath)
	if err != nil {
		t.Fatalf("Failed to read updated lock info: %v", err)
	}

	if !updatedLockInfo.Timestamp.Equal(newTime) {
		t.Errorf("Expected updated timestamp %v, got %v", newTime, updatedLockInfo.Timestamp)
	}
}

func TestMockProcessManager(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	mockFS := &mockFileSystem{files: make(map[string][]byte)}
	mockClock := &mockSystemClock{currentTime: time.Now()}
	mockPM := &mockProcessManager{
		pid:               2000,
		existingProcesses: map[int]bool{1000: true}, // Simulate existing process
		killCalled:        make(map[int]bool),
	}

	deps := &Dependencies{
		FileSystem:     mockFS,
		Clock:          mockClock,
		ProcessManager: mockPM,
	}

	// Create stale lock from another process
	staleLockInfo := LockInfo{
		ProcessID: 1000,
		Timestamp: mockClock.currentTime.Add(-2 * time.Hour),
	}
	data, _ := json.Marshal(staleLockInfo)
	mockFS.files[lockPath] = data

	// Acquire should kill the stale process
	lock, err := Acquire(lockPath, Options{
		StaleTimeout: time.Hour,
		Dependencies: deps,
	})
	if err != nil {
		t.Fatalf("Failed to acquire stale lock: %v", err)
	}
	defer lock.Release()

	// Verify mock process manager was used
	if !mockPM.killCalled[1000] {
		t.Error("Mock process manager should have been called to kill process 1000")
	}

	// Verify new lock has mock PID
	newLockInfo, err := mockFS.ReadLockFile(lockPath)
	if err != nil {
		t.Fatalf("Failed to read new lock info: %v", err)
	}

	if newLockInfo.ProcessID != 2000 {
		t.Errorf("Expected new lock to have PID 2000, got %d", newLockInfo.ProcessID)
	}
}

func TestPartialMocking(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Use real filesystem and clock, mock process manager only
	mockPM := &mockProcessManager{pid: 3000}

	deps := &Dependencies{
		FileSystem:     nil, // Use default (real filesystem)
		Clock:          nil, // Use default (real clock)
		ProcessManager: mockPM,
	}

	// Acquire lock with partial mocking
	lock, err := Acquire(lockPath, Options{Dependencies: deps})
	if err != nil {
		t.Fatalf("Failed to acquire lock with partial mocking: %v", err)
	}
	defer lock.Release()

	// Verify real file was created
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		t.Error("Real file should be created when using real filesystem")
	}

	// Verify mock process manager PID was used
	fs := &defaultFileSystem{}
	lockInfo, err := fs.ReadLockFile(lockPath)
	if err != nil {
		t.Fatalf("Failed to read lock info from real file: %v", err)
	}

	if lockInfo.ProcessID != 3000 {
		t.Errorf("Expected mock PID 3000, got %d", lockInfo.ProcessID)
	}

	// Verify timestamp is recent (real clock)
	if time.Since(lockInfo.Timestamp) > time.Second {
		t.Error("Timestamp should be recent when using real clock")
	}
}

func TestNilDependencies(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Test with completely nil dependencies
	lock, err := Acquire(lockPath, Options{Dependencies: nil})
	if err != nil {
		t.Fatalf("Failed to acquire lock with nil dependencies: %v", err)
	}
	defer lock.Release()

	// Should use defaults and work normally
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		t.Error("Lock file should be created with default filesystem")
	}
}

func TestMockingComplexScenarios(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Scenario: Multiple processes with controlled time progression
	baseTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)

	// Process 1 acquires lock
	mockFS := &mockFileSystem{files: make(map[string][]byte)}
	mockClock1 := &mockSystemClock{currentTime: baseTime}
	mockPM1 := &mockProcessManager{pid: 1001}

	deps1 := &Dependencies{
		FileSystem:     mockFS,
		Clock:          mockClock1,
		ProcessManager: mockPM1,
	}

	lock1, err := Acquire(lockPath, Options{Dependencies: deps1})
	if err != nil {
		t.Fatalf("Process 1 failed to acquire lock: %v", err)
	}

	// Simulate process 1 becoming stale
	mockClock1.currentTime = baseTime.Add(3 * time.Hour)

	// Process 2 tries to acquire with different time and process manager
	mockClock2 := &mockSystemClock{currentTime: baseTime.Add(3 * time.Hour)}
	mockPM2 := &mockProcessManager{
		pid:               1002,
		existingProcesses: map[int]bool{1001: false}, // Process 1001 no longer exists
		killCalled:        make(map[int]bool),
	}

	deps2 := &Dependencies{
		FileSystem:     mockFS, // Same filesystem
		Clock:          mockClock2,
		ProcessManager: mockPM2,
	}

	// Process 2 should be able to acquire stale lock
	lock2, err := Acquire(lockPath, Options{
		StaleTimeout: time.Hour,
		Dependencies: deps2,
	})
	if err != nil {
		t.Fatalf("Process 2 failed to acquire stale lock: %v", err)
	}

	// Verify process 1 was not killed (doesn't exist)
	if mockPM2.killCalled[1001] {
		t.Error("Should not try to kill non-existent process")
	}

	// Verify process 2 owns the lock
	lockInfo, err := mockFS.ReadLockFile(lockPath)
	if err != nil {
		t.Fatalf("Failed to read final lock info: %v", err)
	}

	if lockInfo.ProcessID != 1002 {
		t.Errorf("Expected process 1002 to own lock, got %d", lockInfo.ProcessID)
	}

	// Clean up
	lock1.Release() // Should be safe to call
	lock2.Release()
}

func TestMockingWithRealWorldTiming(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Test HeartBeat interval behavior with mock clock
	mockFS := &mockFileSystem{files: make(map[string][]byte)}
	mockClock := &mockSystemClock{currentTime: time.Now()}
	mockPM := &mockProcessManager{pid: 1003}

	deps := &Dependencies{
		FileSystem:     mockFS,
		Clock:          mockClock,
		ProcessManager: mockPM,
	}

	lock, err := Acquire(lockPath, Options{
		HeartBeatMinimalInterval: 5 * time.Minute,
		Dependencies:             deps,
	})
	if err != nil {
		t.Fatalf("Failed to acquire lock: %v", err)
	}
	defer lock.Release()

	// Get initial timestamp
	initialLock, err := mockFS.ReadLockFile(lockPath)
	if err != nil {
		t.Fatalf("Failed to read initial lock: %v", err)
	}

	// Advance time by 2 minutes (less than interval)
	mockClock.currentTime = mockClock.currentTime.Add(2 * time.Minute)
	err = lock.HeartBeat()
	if err != nil {
		t.Fatalf("HeartBeat failed: %v", err)
	}

	// Timestamp should not change (interval not reached)
	unchangedLock, err := mockFS.ReadLockFile(lockPath)
	if err != nil {
		t.Fatalf("Failed to read unchanged lock: %v", err)
	}

	if !unchangedLock.Timestamp.Equal(initialLock.Timestamp) {
		t.Error("Timestamp should not change when interval not reached")
	}

	// Advance time by 6 minutes total (more than interval)
	mockClock.currentTime = mockClock.currentTime.Add(4 * time.Minute)
	err = lock.HeartBeat()
	if err != nil {
		t.Fatalf("HeartBeat failed: %v", err)
	}

	// Timestamp should now change
	updatedLock, err := mockFS.ReadLockFile(lockPath)
	if err != nil {
		t.Fatalf("Failed to read updated lock: %v", err)
	}

	if updatedLock.Timestamp.Equal(initialLock.Timestamp) {
		t.Error("Timestamp should change when interval is reached")
	}
}