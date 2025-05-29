package livinglock

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultFileSystem_ReadLockFile(t *testing.T) {
	fs := &defaultFileSystem{}
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Test reading non-existent file
	_, err := fs.ReadLockFile(lockPath)
	if !os.IsNotExist(err) {
		t.Errorf("Expected os.ErrNotExist for non-existent file, got: %v", err)
	}

	// Create test lock file
	lockInfo := LockInfo{
		ProcessID: 12345,
		Timestamp: time.Now(),
	}
	data, err := json.Marshal(lockInfo)
	if err != nil {
		t.Fatalf("Failed to marshal lock info: %v", err)
	}
	if err := os.WriteFile(lockPath, data, 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// Test reading valid file
	result, err := fs.ReadLockFile(lockPath)
	if err != nil {
		t.Fatalf("Failed to read lock file: %v", err)
	}
	if result.ProcessID != lockInfo.ProcessID {
		t.Errorf("Expected ProcessID %d, got %d", lockInfo.ProcessID, result.ProcessID)
	}
	if result.Timestamp.Unix() != lockInfo.Timestamp.Unix() {
		t.Errorf("Expected timestamp %v, got %v", lockInfo.Timestamp, result.Timestamp)
	}
}

func TestDefaultFileSystem_WriteLockFile(t *testing.T) {
	fs := &defaultFileSystem{}
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	lockInfo := LockInfo{
		ProcessID: 67890,
		Timestamp: time.Now(),
	}

	// Test writing lock file
	err := fs.WriteLockFile(lockPath, lockInfo)
	if err != nil {
		t.Fatalf("Failed to write lock file: %v", err)
	}

	// Verify file exists and has correct content
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("Failed to read written file: %v", err)
	}

	var result LockInfo
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Failed to unmarshal written data: %v", err)
	}

	if result.ProcessID != lockInfo.ProcessID {
		t.Errorf("Expected ProcessID %d, got %d", lockInfo.ProcessID, result.ProcessID)
	}
	if result.Timestamp.Unix() != lockInfo.Timestamp.Unix() {
		t.Errorf("Expected timestamp %v, got %v", lockInfo.Timestamp, result.Timestamp)
	}
}

func TestDefaultFileSystem_RemoveLockFile(t *testing.T) {
	fs := &defaultFileSystem{}
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Test removing non-existent file (should not error)
	err := fs.RemoveLockFile(lockPath)
	if err != nil {
		t.Errorf("Removing non-existent file should not error, got: %v", err)
	}

	// Create test file
	if err := os.WriteFile(lockPath, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Test removing existing file
	err = fs.RemoveLockFile(lockPath)
	if err != nil {
		t.Fatalf("Failed to remove lock file: %v", err)
	}

	// Verify file is gone
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("File should be removed")
	}
}

func TestDefaultFileSystem_FileNotExists(t *testing.T) {
	fs := &defaultFileSystem{}
	
	// Test with non-existent directory
	_, err := fs.ReadLockFile("/non/existent/path/file.lock")
	if !os.IsNotExist(err) {
		t.Errorf("Expected os.ErrNotExist, got: %v", err)
	}
}

func TestDefaultFileSystem_InvalidPath(t *testing.T) {
	fs := &defaultFileSystem{}
	
	// Test with invalid path (assuming /dev/null/invalid is invalid on most systems)
	_, err := fs.ReadLockFile("/dev/null/invalid")
	if err == nil {
		t.Error("Expected error for invalid path")
	}
}

func TestDefaultFileSystem_PermissionDenied(t *testing.T) {
	fs := &defaultFileSystem{}
	
	// Test with path that requires root permissions
	lockInfo := LockInfo{ProcessID: 123, Timestamp: time.Now()}
	err := fs.WriteLockFile("/root/test.lock", lockInfo)
	if err == nil {
		t.Skip("Skipping permission test - running as root or path is writable")
	}
	// Note: This test may be skipped if running with sufficient permissions
}

func TestDefaultFileSystem_JSONMarshaling(t *testing.T) {
	fs := &defaultFileSystem{}
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Write corrupted JSON
	if err := os.WriteFile(lockPath, []byte("invalid json"), 0644); err != nil {
		t.Fatalf("Failed to write corrupted file: %v", err)
	}

	// Reading corrupted JSON should return os.ErrNotExist
	_, err := fs.ReadLockFile(lockPath)
	if !os.IsNotExist(err) {
		t.Errorf("Expected os.ErrNotExist for corrupted JSON, got: %v", err)
	}

	// Write empty file
	if err := os.WriteFile(lockPath, []byte(""), 0644); err != nil {
		t.Fatalf("Failed to write empty file: %v", err)
	}

	// Reading empty file should return os.ErrNotExist
	_, err = fs.ReadLockFile(lockPath)
	if !os.IsNotExist(err) {
		t.Errorf("Expected os.ErrNotExist for empty file, got: %v", err)
	}

	// Write partial JSON
	if err := os.WriteFile(lockPath, []byte(`{"process_id":`), 0644); err != nil {
		t.Fatalf("Failed to write partial JSON: %v", err)
	}

	// Reading partial JSON should return os.ErrNotExist
	_, err = fs.ReadLockFile(lockPath)
	if !os.IsNotExist(err) {
		t.Errorf("Expected os.ErrNotExist for partial JSON, got: %v", err)
	}
}

func TestDefaultSystemClock_Now(t *testing.T) {
	clock := &defaultSystemClock{}
	
	start := time.Now()
	result := clock.Now()
	end := time.Now()

	// Verify the returned time is within reasonable bounds
	if result.Before(start) || result.After(end) {
		t.Errorf("Clock time %v should be between %v and %v", result, start, end)
	}
}

func TestDefaultSystemClock_Consistency(t *testing.T) {
	clock := &defaultSystemClock{}
	
	// Call Now() multiple times rapidly
	times := make([]time.Time, 10)
	for i := range times {
		times[i] = clock.Now()
	}

	// Verify times are monotonic (or at least not decreasing)
	for i := 1; i < len(times); i++ {
		if times[i].Before(times[i-1]) {
			t.Errorf("Time went backwards: %v -> %v", times[i-1], times[i])
		}
	}
}

func TestDefaultProcessManager_GetPID(t *testing.T) {
	pm := &defaultProcessManager{}
	
	pid := pm.GetPID()
	if pid <= 0 {
		t.Errorf("Expected positive PID, got: %d", pid)
	}

	// Compare with os.Getpid()
	expected := os.Getpid()
	if pid != expected {
		t.Errorf("Expected PID %d, got %d", expected, pid)
	}
}

func TestDefaultProcessManager_Exists(t *testing.T) {
	pm := &defaultProcessManager{}
	
	// Test with current process (should exist)
	currentPID := os.Getpid()
	if !pm.Exists(currentPID) {
		t.Errorf("Current process %d should exist", currentPID)
	}

	// Test with PID 1 (init process, should exist on Unix systems)
	if !pm.Exists(1) {
		t.Log("PID 1 does not exist - this may be normal on some systems")
	}
}

func TestDefaultProcessManager_NonExistentPID(t *testing.T) {
	pm := &defaultProcessManager{}
	
	// Test with very high PID (likely non-existent)
	if pm.Exists(999999) {
		t.Log("PID 999999 exists - this is unexpected but not necessarily wrong")
	}
}

func TestDefaultProcessManager_InvalidPID(t *testing.T) {
	pm := &defaultProcessManager{}
	
	// Test with negative PID
	if pm.Exists(-1) {
		t.Error("Negative PID should not exist")
	}

	// Test with zero PID
	if pm.Exists(0) {
		t.Error("Zero PID should not exist")
	}

	// Test Kill with invalid PID
	err := pm.Kill(-1)
	if err == nil {
		t.Error("Kill with negative PID should return error")
	}

	err = pm.Kill(0)
	if err == nil {
		t.Error("Kill with zero PID should return error")
	}
}

func TestDefaultProcessManager_SelfProcess(t *testing.T) {
	pm := &defaultProcessManager{}
	
	currentPID := pm.GetPID()
	
	// Test existence of self
	if !pm.Exists(currentPID) {
		t.Errorf("Current process %d should exist", currentPID)
	}

	// Note: We don't test Kill on self as it would send SIGHUP to the test process
}

func TestDefaultProcessManager_Kill(t *testing.T) {
	pm := &defaultProcessManager{}
	
	// Create a temporary go file for the sleep process
	tmpDir := t.TempDir()
	sleepFile := filepath.Join(tmpDir, "sleep.go")
	sleepCode := `package main
import "time"
func main() { time.Sleep(30 * time.Second) }`
	
	if err := os.WriteFile(sleepFile, []byte(sleepCode), 0644); err != nil {
		t.Fatalf("Failed to create sleep.go: %v", err)
	}
	
	// Start the sleep process
	cmd := exec.Command("go", "run", sleepFile)
	
	if err := cmd.Start(); err != nil {
		t.Skipf("Failed to start test process (go command may not be available): %v", err)
	}
	
	testPID := cmd.Process.Pid
	defer func() {
		// Cleanup: kill the process if it's still running
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}()
	
	// Give the process a moment to start
	time.Sleep(100 * time.Millisecond)
	
	// Verify process exists
	if !pm.Exists(testPID) {
		t.Fatalf("Test process %d should exist", testPID)
	}
	
	// Send SIGHUP
	err := pm.Kill(testPID)
	if err != nil {
		t.Fatalf("Failed to kill process %d: %v", testPID, err)
	}
	
	// Wait for process to exit (should be killed by SIGHUP)
	err = cmd.Wait()
	if err != nil {
		// Process should exit with non-zero status due to signal
		t.Logf("Process exited as expected due to signal: %v", err)
	} else {
		t.Error("Expected process to exit with non-zero code after SIGHUP")
	}
	
	// Verify process no longer exists
	time.Sleep(100 * time.Millisecond)
	if pm.Exists(testPID) {
		t.Errorf("Process %d should no longer exist after kill", testPID)
	}
}

func TestDefaultDependencies_NilHandling(t *testing.T) {
	// Test with completely nil dependencies
	fs, clock, pm := getOrCreateDefaults(nil)
	
	if fs == nil {
		t.Error("FileSystem should not be nil")
	}
	if clock == nil {
		t.Error("SystemClock should not be nil")
	}
	if pm == nil {
		t.Error("ProcessManager should not be nil")
	}

	// Verify they are default implementations
	if _, ok := fs.(*defaultFileSystem); !ok {
		t.Error("Expected defaultFileSystem")
	}
	if _, ok := clock.(*defaultSystemClock); !ok {
		t.Error("Expected defaultSystemClock")
	}
	if _, ok := pm.(*defaultProcessManager); !ok {
		t.Error("Expected defaultProcessManager")
	}
}

func TestDefaultDependencies_PartialNil(t *testing.T) {
	// Test with partial nil dependencies
	customFS := &mockFileSystem{files: make(map[string][]byte)}
	
	deps := &Dependencies{
		FileSystem:     customFS,
		Clock:          nil, // Should use default
		ProcessManager: nil, // Should use default
	}
	
	fs, clock, pm := getOrCreateDefaults(deps)
	
	// Custom FileSystem should be preserved
	if fs != customFS {
		t.Error("Custom FileSystem should be preserved")
	}
	
	// Others should be defaults
	if _, ok := clock.(*defaultSystemClock); !ok {
		t.Error("Expected defaultSystemClock for nil Clock")
	}
	if _, ok := pm.(*defaultProcessManager); !ok {
		t.Error("Expected defaultProcessManager for nil ProcessManager")
	}
}

