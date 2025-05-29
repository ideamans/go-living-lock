package livinglock

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Integration test using subprocess execution
func TestIntegration_MultipleProcesses(t *testing.T) {
	if os.Getenv("LIVINGLOCK_INTEGRATION_MODE") != "" {
		// This is a child process
		runIntegrationChild()
		return
	}

	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "integration.lock")

	// Launch multiple child processes simultaneously
	const numProcesses = 5
	var processes []*exec.Cmd

	for i := 0; i < numProcesses; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=TestIntegration_MultipleProcesses")
		cmd.Env = append(os.Environ(),
			"LIVINGLOCK_INTEGRATION_MODE=acquire",
			"LOCK_PATH="+lockPath,
			"PROCESS_ID="+strconv.Itoa(i),
		)
		err := cmd.Start()
		if err != nil {
			t.Fatalf("Failed to start process %d: %v", i, err)
		}
		processes = append(processes, cmd)
	}

	// Wait for all processes and collect results
	var successCount, errorCount int
	for i, proc := range processes {
		err := proc.Wait()
		if err != nil {
			// Check if it's our expected "lock busy" error
			if exitError, ok := err.(*exec.ExitError); ok {
				if exitError.ExitCode() == 2 { // Our custom exit code for ErrLockBusy
					errorCount++
				} else {
					t.Errorf("Process %d failed with unexpected error: %v", i, err)
				}
			} else {
				t.Errorf("Process %d failed: %v", i, err)
			}
		} else {
			successCount++
		}
	}

	// Verify that exactly one process succeeded
	if successCount != 1 {
		t.Errorf("Expected exactly 1 success, got %d", successCount)
	}
	if errorCount != numProcesses-1 {
		t.Errorf("Expected %d lock busy errors, got %d", numProcesses-1, errorCount)
	}

	t.Logf("Integration test completed: %d success, %d errors", successCount, errorCount)
}

func TestIntegration_ProcessCrash(t *testing.T) {
	if os.Getenv("LIVINGLOCK_INTEGRATION_MODE") != "" {
		// This is a child process
		runIntegrationChild()
		return
	}

	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "crash-test.lock")

	// Process 1: Acquire lock and simulate crash
	cmd1 := exec.Command(os.Args[0], "-test.run=TestIntegration_ProcessCrash")
	cmd1.Env = append(os.Environ(),
		"LIVINGLOCK_INTEGRATION_MODE=acquire_and_hold",
		"LOCK_PATH="+lockPath,
		"PROCESS_ID=1",
	)

	err := cmd1.Start()
	if err != nil {
		t.Fatalf("Failed to start first process: %v", err)
	}

	// Give first process time to acquire lock
	time.Sleep(200 * time.Millisecond)

	// Simulate crash by killing the process
	err = cmd1.Process.Kill()
	if err != nil {
		t.Fatalf("Failed to kill first process: %v", err)
	}
	cmd1.Wait()

	// Wait for lock to become stale
	time.Sleep(100 * time.Millisecond)

	// Process 2: Should be able to take over stale lock
	cmd2 := exec.Command(os.Args[0], "-test.run=TestIntegration_ProcessCrash")
	cmd2.Env = append(os.Environ(),
		"LIVINGLOCK_INTEGRATION_MODE=acquire_stale",
		"LOCK_PATH="+lockPath,
		"PROCESS_ID=2",
		"STALE_TIMEOUT=50ms", // Short timeout for testing
	)

	output, err := cmd2.Output()
	if err != nil {
		t.Fatalf("Second process failed to acquire stale lock: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(string(output), "STALE_LOCK_ACQUIRED") {
		t.Errorf("Expected stale lock acquisition, got output: %s", output)
	}

	t.Log("Process crash and recovery test completed successfully")
}

func TestIntegration_SystemReboot(t *testing.T) {
	if os.Getenv("LIVINGLOCK_INTEGRATION_MODE") != "" {
		runIntegrationChild()
		return
	}

	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "reboot-test.lock")

	// Simulate system reboot by creating an old lock file with non-existent PID
	oldLockInfo := LockInfo{
		ProcessID: 999999, // Very unlikely to exist
		Timestamp: time.Now().Add(-2 * time.Hour),
	}

	fs := &defaultFileSystem{}
	err := fs.WriteLockFile(lockPath, oldLockInfo)
	if err != nil {
		t.Fatalf("Failed to create old lock file: %v", err)
	}

	// New process should be able to acquire lock from non-existent process
	cmd := exec.Command(os.Args[0], "-test.run=TestIntegration_SystemReboot")
	cmd.Env = append(os.Environ(),
		"LIVINGLOCK_INTEGRATION_MODE=acquire_after_reboot",
		"LOCK_PATH="+lockPath,
		"STALE_TIMEOUT=1h",
	)

	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Process failed to acquire lock after reboot: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(string(output), "REBOOT_RECOVERY_SUCCESS") {
		t.Errorf("Expected reboot recovery success, got output: %s", output)
	}

	t.Log("System reboot simulation test completed successfully")
}

// Child process implementation
func runIntegrationChild() {
	mode := os.Getenv("LIVINGLOCK_INTEGRATION_MODE")
	lockPath := os.Getenv("LOCK_PATH")
	processID := os.Getenv("PROCESS_ID")

	if lockPath == "" {
		fmt.Fprintf(os.Stderr, "LOCK_PATH not set\n")
		os.Exit(1)
	}

	switch mode {
	case "acquire":
		runAcquireTest(lockPath, processID)
	case "acquire_and_hold":
		runAcquireAndHoldTest(lockPath, processID)
	case "acquire_stale":
		runAcquireStaleTest(lockPath, processID)
	case "acquire_after_reboot":
		runAcquireAfterRebootTest(lockPath)
	default:
		fmt.Fprintf(os.Stderr, "Unknown integration mode: %s\n", mode)
		os.Exit(1)
	}
}

func runAcquireTest(lockPath, processID string) {
	lock, err := Acquire(lockPath, Options{})
	if err != nil {
		if err == ErrLockBusy {
			fmt.Printf("Process %s: Lock busy\n", processID)
			os.Exit(2) // Custom exit code for lock busy
		}
		fmt.Fprintf(os.Stderr, "Process %s failed: %v\n", processID, err)
		os.Exit(1)
	}

	fmt.Printf("Process %s: Lock acquired successfully\n", processID)

	// Hold lock briefly
	time.Sleep(100 * time.Millisecond)

	lock.Release()
	fmt.Printf("Process %s: Lock released\n", processID)
	os.Exit(0)
}

func runAcquireAndHoldTest(lockPath, processID string) {
	lock, err := Acquire(lockPath, Options{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Process %s failed to acquire: %v\n", processID, err)
		os.Exit(1)
	}

	fmt.Printf("Process %s: Lock acquired, holding...\n", processID)

	// Hold lock indefinitely (until killed)
	for {
		time.Sleep(100 * time.Millisecond)
		err := lock.HeartBeat()
		if err != nil {
			fmt.Fprintf(os.Stderr, "HeartBeat failed: %v\n", err)
			break
		}
	}
}

func runAcquireStaleTest(lockPath, processID string) {
	staleTimeoutStr := os.Getenv("STALE_TIMEOUT")
	staleTimeout, err := time.ParseDuration(staleTimeoutStr)
	if err != nil {
		staleTimeout = time.Hour // Default
	}

	lock, err := Acquire(lockPath, Options{
		StaleTimeout: staleTimeout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Process %s failed to acquire stale lock: %v\n", processID, err)
		os.Exit(1)
	}

	fmt.Printf("STALE_LOCK_ACQUIRED by process %s\n", processID)
	lock.Release()
	os.Exit(0)
}

func runAcquireAfterRebootTest(lockPath string) {
	lock, err := Acquire(lockPath, Options{
		StaleTimeout: time.Hour,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to acquire lock after reboot: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("REBOOT_RECOVERY_SUCCESS")
	lock.Release()
	os.Exit(0)
}
