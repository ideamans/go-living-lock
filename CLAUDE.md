# livinglock Package Implementation Plan

## 1. Package Purpose and Use Cases

### Purpose

The `livinglock` package provides a robust file-based process locking mechanism specifically designed for **scheduled batch processing** where the same program runs repeatedly. Unlike traditional file locks, `livinglock` implements a "living lock" system that can detect and handle both zombie processes and deadlocked processes that are still running but no longer performing meaningful work.

### Core Problem Solved

**Batch Processing Exclusion with Deadlock Detection**: In scheduled batch environments (cron jobs, scheduled tasks), the same program may be triggered while a previous instance is still running. Traditional approaches have critical flaws:

1. **Simple PID-based locks**: Cannot detect deadlocked processes that are still alive but stuck
2. **Time-based locks**: May terminate healthy long-running processes
3. **No locks**: Allow harmful concurrent executions

`livinglock` solves this by requiring **active heartbeat signals** from the lock holder, proving not just process existence but **actual ongoing work**.

### Key Features

- **Batch-aware exclusion**: Prevents concurrent execution of scheduled batch processes
- **Deadlock detection**: Detects processes that are alive but stuck in deadlocks or infinite loops
- **Active heartbeat mechanism**: Requires explicit proof-of-work signals, not just process existence
- **Graceful priority handover**: New processes yield to actively working processes, take over from deadlocked ones
- **Stale lock recovery**: Automatically recovers from crashed processes and system reboots
- **Configurable timeouts**: Flexible heartbeat intervals and stale detection thresholds
- **Production-ready reliability**: Full dependency injection and comprehensive testing

### Use Cases

#### Primary Use Case: Scheduled Batch Processing

This is the **primary and most important use case** that `livinglock` was designed to solve:

```go
// Daily data processing batch job (e.g., cron: 0 2 * * *)
func main() {
    lock, err := livinglock.Acquire("/var/run/daily-batch.lock", livinglock.Options{
        StaleTimeout:             2 * time.Hour,  // Max expected runtime
        HeartBeatMinimalInterval: 5 * time.Minute, // Heartbeat frequency
    })
    if err != nil {
        if errors.Is(err, livinglock.ErrLockBusy) {
            log.Printf("Previous batch still running, yielding priority")
            return // Gracefully exit, let the active process continue
        }
        log.Fatalf("Failed to acquire lock: %v", err)
    }
    defer lock.Release()

    log.Printf("Starting batch processing...")
    
    // Process large dataset with periodic heartbeats
    for _, batch := range dataBatches {
        // Perform actual work
        processDataBatch(batch)
        
        // Signal that we're actively working (not deadlocked)
        if err := lock.HeartBeat(); err != nil {
            log.Printf("Heartbeat failed, another process may be taking over: %v", err)
            return
        }
        
        log.Printf("Completed batch %d, heartbeat sent", batch.ID)
    }
    
    log.Printf("Batch processing completed successfully")
}
```

**Scenario Handled**:
1. **Normal case**: Cron triggers at 2 AM, previous job finished → new job runs
2. **Long-running case**: Previous job still working → new job yields gracefully  
3. **Deadlock case**: Previous job stuck for >2 hours without heartbeat → new job takes over
4. **Crash case**: Previous job crashed → new job detects stale lock and takes over

#### Traditional Scheduled Jobs

```go
// Backup process that may take variable time
lock, err := livinglock.Acquire("/tmp/backup-job.lock", livinglock.Options{
    StaleTimeout: 4 * time.Hour, // Backup jobs can take a while
})
if err != nil {
    if errors.Is(err, livinglock.ErrLockBusy) {
        log.Printf("Backup already in progress, skipping")
        return
    }
    log.Fatalf("Backup failed to start: %v", err)
}
defer lock.Release()

// Long-running backup process with periodic heartbeats
for _, dataset := range datasets {
    backupDataset(dataset)
    
    // Prove we're still actively working
    if err := lock.HeartBeat(); err != nil {
        log.Printf("Heartbeat failed: %v", err)
        break
    }
}
```

#### Secondary Use Cases

**Development Environment**:
```go
// Prevent multiple development servers on same port
lock, err := livinglock.Acquire("/tmp/dev-server-8080.lock", livinglock.Options{
    StaleTimeout: 30 * time.Minute,
    HeartBeatMinimalInterval: 5 * time.Minute,
})
```

**Database Migration Scripts**:
```go
// Ensure only one migration runs at a time
lock, err := livinglock.Acquire("/tmp/db-migration.lock", livinglock.Options{
    StaleTimeout: 2 * time.Hour,
})
```

### Design Philosophy

#### Why Not Simple PID Locks?

Traditional PID-based locks fail in common batch scenarios:

```bash
# Process exists but is deadlocked (stuck forever)
$ ps aux | grep daily-batch
user  12345  0.0  0.1  process-stuck-in-deadlock

# New cron job cannot start even though no real work is happening
$ cron: daily-batch.lock exists, process 12345 alive → skip execution
```

#### Why Not Time-Based Locks?

Fixed timeout locks are too rigid for variable-duration batch jobs:

```bash
# Healthy long-running job gets killed prematurely
$ timeout 1h daily-batch  # Kills healthy 2-hour data processing job
```

#### The Living Lock Solution

Combines **process aliveness** with **proof of ongoing work**:

```bash
# Process exists AND is actively sending heartbeats → healthy, don't interfere
# Process exists BUT no heartbeats for StaleTimeout → deadlocked, safe to take over
# Process doesn't exist → crashed, safe to take over
```

## 2. Comprehensive Test Scenarios

### Test File Breakdown

#### livinglock_basic_test.go

- **Test_Acquire_NewLock**: First process acquires lock successfully
- **Test_Acquire_SamePID**: Same process can re-acquire its own lock
- **Test_Acquire_DifferentPID_Active**: Different process cannot acquire active lock
- **Test_Release**: Lock is properly released and file is deleted
- **Test_Release_Multiple**: Multiple releases are safe (no error)

#### livinglock_stale_test.go

- **Test_Acquire_StaleLock**: Old lock is taken over when timestamp is stale
- **Test_Acquire_StaleLock_ProcessExists**: Stale lock triggers kill signal if process exists
- **Test_Acquire_StaleLock_ProcessNotExists**: Stale lock is taken over when process doesn't exist
- **Test_StaleTimeout_Boundary**: Test exact boundary conditions for stale timeout

#### livinglock_heartbeat_test.go

- **Test_HeartBeat_UpdatesTimestamp**: HeartBeat updates lock file timestamp
- **Test_HeartBeat_RespectInterval**: HeartBeat respects update interval setting
- **Test_HeartBeat_IntervalZero**: HeartBeat updates every time when interval is 0
- **Test_HeartBeat_AfterRelease**: HeartBeat is silently ignored after release
- **Test_HeartBeat_Concurrent**: Multiple goroutines calling HeartBeat safely

#### livinglock_error_test.go

- **Test_HeartBeat_FileDisappeared**: Panic when lock file disappears
- **Test_HeartBeat_FileHijacked**: Panic when lock file PID changes
- **Test_Acquire_WriteError**: Handle lock file write errors
- **Test_Release_RemoveError**: Handle lock file removal errors
- **Test_RapidAcquireRelease**: Rapid acquire/release cycles
- **Test_FilePermissions**: Handle file permission errors

#### livinglock_corruption_test.go

- **Test_Acquire_InvalidJSON**: Corrupted JSON is treated as no lock file
- **Test_Acquire_EmptyFile**: Empty file is treated as no lock file
- **Test_Acquire_PartialJSON**: Partial JSON is treated as no lock file

#### livinglock_mock_test.go

- **Test_MockFileSystem**: Test with mocked file operations
- **Test_MockClock**: Test with controlled time progression
- **Test_MockProcessManager**: Test with mocked process operations
- **Test_PartialMocking**: Test mixing real and mocked dependencies
- **Test_NilDependencies**: Test default behavior with nil dependencies

#### livinglock_integration_test.go

- **Test_RealScenario_MultipleProcesses**: Spawn actual processes for integration testing
- **Test_RealScenario_ProcessCrash**: Test behavior when process crashes
- **Test_RealScenario_SystemReboot**: Test behavior after system restart
- **Test_DiskFull**: Handle disk full scenarios
- **Test_NetworkDrive**: Behavior on network-mounted filesystems
- **Test_ProcessSignaling**: SIGKILL signal handling

#### livinglock_defaults_test.go

- **Test_DefaultFileSystem_ReadLockFile**: Test real file system read operations
- **Test_DefaultFileSystem_WriteLockFile**: Test real file system write operations
- **Test_DefaultFileSystem_RemoveLockFile**: Test real file system remove operations
- **Test_DefaultFileSystem_FileNotExists**: Test behavior when lock file doesn't exist
- **Test_DefaultFileSystem_InvalidPath**: Test behavior with invalid file paths
- **Test_DefaultFileSystem_PermissionDenied**: Test behavior with permission errors
- **Test_DefaultFileSystem_JSONMarshaling**: Test JSON encoding/decoding with real files
- **Test_DefaultSystemClock_Now**: Test real time operations and precision
- **Test_DefaultSystemClock_Consistency**: Test clock consistency across calls
- **Test_DefaultProcessManager_GetPID**: Test real process ID retrieval
- **Test_DefaultProcessManager_Exists**: Test real process existence checking
- **Test_DefaultProcessManager_Kill**: Test real process signaling (SIGHUP)
- **Test_DefaultProcessManager_NonExistentPID**: Test behavior with non-existent PIDs
- **Test_DefaultProcessManager_InvalidPID**: Test behavior with invalid PIDs (negative, zero)
- **Test_DefaultProcessManager_SelfProcess**: Test operations on current process
- **Test_DefaultDependencies_NilHandling**: Test nil dependency fallback to defaults
- **Test_DefaultDependencies_PartialNil**: Test partial nil dependencies mixed with custom ones

## 3. File Structure

```
livinglock/
├── livinglock.go                      # Main package implementation
├── livinglock_basic_test.go          # Basic functionality tests
├── livinglock_stale_test.go          # Stale lock detection tests
├── livinglock_heartbeat_test.go      # HeartBeat functionality tests
├── livinglock_error_test.go          # Error handling tests
├── livinglock_corruption_test.go     # Corrupted file handling tests
├── livinglock_mock_test.go           # Dependency injection tests
├── livinglock_defaults_test.go       # Default implementation tests
├── livinglock_integration_test.go    # Integration tests with real processes
├── testdata/                         # Test fixtures and mock implementations
│   ├── mock_filesystem.go            # Mock FileSystem implementation
│   ├── mock_clock.go                 # Mock SystemClock implementation
│   └── mock_process.go               # Mock ProcessManager implementation
├── examples/                         # Usage examples
│   ├── basic/                        # Simple usage example
│   │   └── main.go
│   ├── daemon/                       # Daemon service example
│   │   └── main.go
│   └── scheduled_job/                # Cron job example
│       └── main.go
├── go.mod                           # Go module definition
├── go.sum                           # Go module checksums
├── README.md                        # User documentation
├── CHANGELOG.md                     # Version history
└── CLAUDE.md                       # This implementation plan
```

### File Descriptions

#### Core Files

- **livinglock.go**: Main package containing all interfaces, structs, and core logic

#### Test Files

- **livinglock_basic_test.go**: Basic functionality tests (acquire, release, same PID)
- **livinglock_stale_test.go**: Stale lock detection and timeout tests
- **livinglock_heartbeat_test.go**: HeartBeat functionality and heartbeat tests
- **livinglock_error_test.go**: Error handling and edge case tests
- **livinglock_corruption_test.go**: Corrupted lock file handling tests
- **livinglock_mock_test.go**: Dependency injection and mocking tests
- **livinglock_defaults_test.go**: Default implementation tests for real dependencies
- **livinglock_integration_test.go**: End-to-end tests with real processes

#### Test Support

- **testdata/**: Mock implementations for dependency injection during testing

#### Documentation and Examples

- **examples/**: Practical usage examples for different scenarios
- **README.md**: User-facing documentation with API reference
- **CLAUDE.md**: This implementation plan and design rationale

## 4. Reference Implementation Code

The following code serves as the initial design specification for the `livinglock` package. This represents the core structure and interfaces that should be implemented:

```go
package livinglock

import (
 "encoding/json"
 "fmt"
 "os"
 "sync"
 "time"
)

// LockInfo represents the content of a lock file
type LockInfo struct {
 ProcessID int       `json:"process_id"`
 Timestamp time.Time `json:"timestamp"`
}

// FileSystem interface for file operations
type FileSystem interface {
 ReadLockFile(filePath string) (LockInfo, error)
 WriteLockFile(filePath string, lockInfo LockInfo) error
 RemoveLockFile(filePath string) error
}

// SystemClock interface for time operations
type SystemClock interface {
 Now() time.Time
}

// ProcessManager interface for process operations
type ProcessManager interface {
 GetPID() int
 Exists(pid int) bool
 Kill(pid int) error // sends SIGKILL
}

// Dependencies holds all external dependencies (nil values use defaults)
type Dependencies struct {
 FileSystem     FileSystem
 Clock          SystemClock
 ProcessManager ProcessManager
}

// Options for lock configuration
type Options struct {
 StaleTimeout         time.Duration // default: 1 hour
 HeartBeatMinimalInterval time.Duration // default: 1 minute
 Dependencies         *Dependencies // optional for testing
}

// Lock represents an acquired lock
type Lock struct {
 filePath             string
 options              Options
 fs                   FileSystem
 clock                SystemClock
 pm                   ProcessManager
 mu                   sync.Mutex
 released             bool
 lastBeacon           time.Time
}

// Acquire attempts to acquire a lock at the specified file path
func Acquire(filePath string, options Options) (*Lock, error) {
 // Implementation details...
}

// HeartBeat signals that the process is still alive and updates the lock file if needed
func (l *Lock) HeartBeat() error {
 // Implementation details...
}

// Release releases the lock and removes the lock file
func (l *Lock) Release() error {
 // Implementation details...
}
```

### Implementation Notes

1. **Error Handling**: Corrupted lock files (invalid JSON) should be treated as non-existent locks
2. **Concurrency**: The `HeartBeat()` method should use minimal locking for performance
3. **Graceful Shutdown**: `HeartBeat()` calls after `Release()` should be silently ignored
4. **Process Signaling**: Use `SIGKILL` for zombie process cleanup
5. **Default Values**: Provide sensible defaults (1 hour stale timeout, 1 minute heartbeat interval)
6. **Dependency Injection**: Support partial mocking - nil dependencies should use default implementations

### Testing Strategy

- **Unit Tests**: Focus on logic with mocked dependencies
- **Integration Tests**: Test with real file system and processes
- **Edge Cases**: Handle file corruption, permission errors, disk full scenarios
- **Concurrency**: Test multiple goroutines using the same lock
- **Error Conditions**: Ensure proper panic behavior for lock hijacking

This design prioritizes simplicity, testability, and real-world usability while maintaining robust error handling and performance characteristics.
