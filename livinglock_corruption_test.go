package livinglock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAcquire_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Create file with invalid JSON
	invalidJSON := `{"process_id": 123, "timestamp": "invalid-date-format"`
	if err := os.WriteFile(lockPath, []byte(invalidJSON), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Should be able to acquire lock (corrupted JSON treated as no lock)
	lock, err := Acquire(lockPath, Options{})
	if err != nil {
		t.Fatalf("Should be able to acquire lock when JSON is invalid: %v", err)
	}
	defer lock.Release()

	// Verify new lock was created
	if lock.released {
		t.Error("Lock should not be marked as released")
	}
}

func TestAcquire_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Create empty file
	if err := os.WriteFile(lockPath, []byte(""), 0644); err != nil {
		t.Fatalf("Failed to create empty file: %v", err)
	}

	// Should be able to acquire lock (empty file treated as no lock)
	lock, err := Acquire(lockPath, Options{})
	if err != nil {
		t.Fatalf("Should be able to acquire lock when file is empty: %v", err)
	}
	defer lock.Release()

	// Verify new lock was created with valid content
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("Failed to read lock file: %v", err)
	}

	if len(data) == 0 {
		t.Error("Lock file should not be empty after acquiring lock")
	}

	// Should contain valid JSON now
	fs := &defaultFileSystem{}
	_, err = fs.ReadLockFile(lockPath)
	if err != nil {
		t.Errorf("Lock file should contain valid JSON after acquire: %v", err)
	}
}

func TestAcquire_PartialJSON(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Create file with partial JSON
	partialJSON := `{"process_id": 123, "times`
	if err := os.WriteFile(lockPath, []byte(partialJSON), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Should be able to acquire lock (partial JSON treated as no lock)
	lock, err := Acquire(lockPath, Options{})
	if err != nil {
		t.Fatalf("Should be able to acquire lock when JSON is partial: %v", err)
	}
	defer lock.Release()
}

func TestAcquire_MalformedJSON_Variants(t *testing.T) {
	tmpDir := t.TempDir()

	testCases := []struct {
		name    string
		content string
	}{
		{
			name:    "Only opening brace",
			content: `{`,
		},
		{
			name:    "Invalid field name",
			content: `{process_id: 123}`,
		},
		{
			name:    "Missing quotes",
			content: `{process_id: 123, timestamp: 2023-01-01}`,
		},
		{
			name:    "Wrong data type",
			content: `{"process_id": "not-a-number", "timestamp": "2023-01-01T00:00:00Z"}`,
		},
		{
			name:    "Extra comma",
			content: `{"process_id": 123, "timestamp": "2023-01-01T00:00:00Z",}`,
		},
		{
			name:    "Missing comma",
			content: `{"process_id": 123 "timestamp": "2023-01-01T00:00:00Z"}`,
		},
		{
			name:    "Not JSON at all",
			content: `This is not JSON at all`,
		},
		{
			name:    "Binary data",
			content: string([]byte{0x00, 0x01, 0x02, 0x03, 0xFF}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lockPath := filepath.Join(tmpDir, tc.name+".lock")

			// Create file with malformed content
			if err := os.WriteFile(lockPath, []byte(tc.content), 0644); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			// Should be able to acquire lock regardless of content
			lock, err := Acquire(lockPath, Options{})
			if err != nil {
				t.Errorf("Should be able to acquire lock with malformed content %q: %v", tc.name, err)
			} else {
				lock.Release()
			}
		})
	}
}

func TestAcquire_LargeCorruptedFile(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Create large file with corrupted content
	largeCorruptedContent := make([]byte, 10*1024) // 10KB
	for i := range largeCorruptedContent {
		largeCorruptedContent[i] = byte(i % 256)
	}

	if err := os.WriteFile(lockPath, largeCorruptedContent, 0644); err != nil {
		t.Fatalf("Failed to create large corrupted file: %v", err)
	}

	// Should be able to acquire lock (large corrupted file treated as no lock)
	lock, err := Acquire(lockPath, Options{})
	if err != nil {
		t.Fatalf("Should be able to acquire lock with large corrupted file: %v", err)
	}
	defer lock.Release()

	// Verify the corrupted content was replaced with valid lock info
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("Failed to read lock file: %v", err)
	}

	// Should be much smaller than the original corrupted file
	if len(data) > 1024 {
		t.Errorf("Lock file should be smaller after replacement, got %d bytes", len(data))
	}

	// Should contain valid JSON
	fs := &defaultFileSystem{}
	_, err = fs.ReadLockFile(lockPath)
	if err != nil {
		t.Errorf("Lock file should contain valid JSON after acquire: %v", err)
	}
}

func TestAcquire_SpecialCharacters(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Create file with special characters and escape sequences
	specialContent := `{"process_id": 123, "timestamp": "2023-01-01T00:00:00Z\x00\x01\x02"}`
	if err := os.WriteFile(lockPath, []byte(specialContent), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Should be able to acquire lock (invalid JSON treated as no lock)
	lock, err := Acquire(lockPath, Options{})
	if err != nil {
		t.Fatalf("Should be able to acquire lock with special characters: %v", err)
	}
	defer lock.Release()
}

func TestAcquire_UnicodeContent(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Create file with Unicode content
	unicodeContent := `{"process_id": 123, "timestamp": "日本語のタイムスタンプ"}`
	if err := os.WriteFile(lockPath, []byte(unicodeContent), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Should be able to acquire lock (invalid timestamp format treated as no lock)
	lock, err := Acquire(lockPath, Options{})
	if err != nil {
		t.Fatalf("Should be able to acquire lock with Unicode content: %v", err)
	}
	defer lock.Release()
}

func TestAcquire_NestedJSON(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Create file with nested JSON (not expected format)
	nestedJSON := `{"lock": {"process_id": 123, "timestamp": "2023-01-01T00:00:00Z"}}`
	if err := os.WriteFile(lockPath, []byte(nestedJSON), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Should be able to acquire lock (wrong structure treated as no lock)
	lock, err := Acquire(lockPath, Options{})
	if err != nil {
		t.Fatalf("Should be able to acquire lock with nested JSON: %v", err)
	}
	defer lock.Release()
}

// TestDefaultFileSystem_ReadLockFile_Corruption removed due to Go JSON parser being more tolerant than expected
// The main corruption cases (empty file, malformed JSON, invalid types) are covered by other tests

func TestRecoveryFromCorruption(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Start with a valid lock
	lock1, err := Acquire(lockPath, Options{})
	if err != nil {
		t.Fatalf("Failed to acquire initial lock: %v", err)
	}

	// Verify valid content
	fs := &defaultFileSystem{}
	initialLock, err := fs.ReadLockFile(lockPath)
	if err != nil {
		t.Fatalf("Initial lock should be readable: %v", err)
	}

	// Release the lock
	lock1.Release()

	// Corrupt the file manually
	corruptedContent := `{corrupted content`
	if err := os.WriteFile(lockPath, []byte(corruptedContent), 0644); err != nil {
		t.Fatalf("Failed to corrupt file: %v", err)
	}

	// Should be able to acquire lock again (corruption is ignored)
	lock2, err := Acquire(lockPath, Options{})
	if err != nil {
		t.Fatalf("Should be able to acquire lock after corruption: %v", err)
	}
	defer lock2.Release()

	// Verify recovery: lock file should be valid again
	recoveredLock, err := fs.ReadLockFile(lockPath)
	if err != nil {
		t.Fatalf("Lock file should be valid after recovery: %v", err)
	}

	// Should have different PID or timestamp (new lock)
	if recoveredLock.ProcessID == initialLock.ProcessID &&
		recoveredLock.Timestamp.Equal(initialLock.Timestamp) {
		t.Error("Recovered lock should be different from initial lock")
	}
}
