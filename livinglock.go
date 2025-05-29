// Package livinglock provides a robust file-based process locking mechanism that
// prevents multiple instances of the same application from running simultaneously.
// Unlike traditional file locks, livinglock implements a "living lock" system that
// can detect and handle zombie processes - processes that have stopped functioning
// but haven't properly released their locks.
//
// The package is specifically designed for scheduled batch processing where processes
// need to ensure exclusive execution and detect deadlocked processes that are alive
// but no longer performing meaningful work.
//
// Key features:
//   - Process exclusion: Ensures only one instance of an application runs at a time
//   - Zombie process detection: Automatically detects and handles stale locks from crashed processes
//   - Heartbeat mechanism: Regular heartbeat updates prove process liveness
//   - Graceful takeover: Can safely take over locks from crashed processes
//   - Testable design: Full dependency injection for comprehensive testing
//
// Basic usage:
//
//	lock, err := livinglock.Acquire("/var/run/mydaemon.lock", livinglock.Options{})
//	if err != nil {
//		log.Fatalf("Another instance is running: %v", err)
//	}
//	defer lock.Release()
//
//	// Long-running process with periodic heartbeats
//	for {
//		// Do work...
//		if err := lock.HeartBeat(); err != nil {
//			log.Printf("Failed to update heartbeat: %v", err)
//			break
//		}
//		time.Sleep(time.Minute)
//	}
package livinglock

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
)

// ErrLockBusy is returned when a lock is held by another active process.
// This error indicates that the lock acquisition failed because another
// process currently holds the lock and is actively maintaining it through
// heartbeat updates.
var ErrLockBusy = errors.New("lock is held by another process")

// LockInfo represents the content of a lock file.
// It contains the process ID of the lock holder and the timestamp
// of the last heartbeat update, which is used to determine if
// the lock is stale.
type LockInfo struct {
	// ProcessID is the process ID of the lock holder
	ProcessID int `json:"process_id"`
	// Timestamp is the time of the last heartbeat update
	Timestamp time.Time `json:"timestamp"`
}

// FileSystem interface abstracts file operations for lock management.
// This interface allows for dependency injection and testing with
// mock implementations.
type FileSystem interface {
	// ReadLockFile reads and parses a lock file, returning the lock information.
	// Returns os.ErrNotExist if the file doesn't exist or contains invalid JSON.
	ReadLockFile(filePath string) (LockInfo, error)

	// WriteLockFile writes lock information to the specified file.
	// Creates the file if it doesn't exist, overwrites if it does.
	WriteLockFile(filePath string, lockInfo LockInfo) error

	// RemoveLockFile removes the lock file.
	// Returns nil if the file doesn't exist (idempotent operation).
	RemoveLockFile(filePath string) error
}

// SystemClock interface abstracts time operations.
// This interface allows for testing with controlled time progression
// and ensures consistent time handling across the package.
type SystemClock interface {
	// Now returns the current time
	Now() time.Time
}

// ProcessManager interface abstracts process operations.
// This interface allows for testing process management functionality
// without affecting real processes.
type ProcessManager interface {
	// GetPID returns the current process ID
	GetPID() int

	// Exists checks if a process with the given PID exists
	Exists(pid int) bool

	// Kill sends SIGKILL to the process with the given PID.
	// Used to terminate zombie processes holding stale locks.
	Kill(pid int) error
}

// Dependencies holds all external dependencies for dependency injection.
// Nil values will be replaced with default implementations.
// This structure is primarily used for testing but can also be used
// to customize behavior in production environments.
type Dependencies struct {
	// FileSystem handles file operations (nil uses defaultFileSystem)
	FileSystem FileSystem

	// Clock provides time operations (nil uses defaultSystemClock)
	Clock SystemClock

	// ProcessManager handles process operations (nil uses defaultProcessManager)
	ProcessManager ProcessManager
}

// Options configures the behavior of lock acquisition and maintenance.
type Options struct {
	// StaleTimeout is the duration after which a lock is considered stale
	// if no heartbeat updates have been received. Default: 1 hour.
	// When a stale lock is detected, the holding process may be terminated.
	StaleTimeout time.Duration

	// HeartBeatMinimalInterval is the minimum interval between heartbeat updates.
	// HeartBeat() calls more frequent than this interval will be ignored.
	// Default: 1 minute. Set to 0 to update on every HeartBeat() call.
	HeartBeatMinimalInterval time.Duration

	// Dependencies allows injection of custom implementations for testing.
	// Nil dependencies will use default implementations.
	Dependencies *Dependencies
}

// Lock represents an acquired lock and provides methods to maintain
// and release it. A Lock instance is returned by successful Acquire()
// calls and should be used to send heartbeat updates and eventually
// release the lock.
//
// Lock instances are safe for concurrent use by multiple goroutines.
type Lock struct {
	filePath      string         // Path to the lock file
	options       Options        // Configuration options
	fs            FileSystem     // File system operations
	clock         SystemClock    // Time operations
	pm            ProcessManager // Process operations
	mu            sync.Mutex     // Protects concurrent access
	released      bool           // Whether the lock has been released
	lastHeartBeat time.Time      // Timestamp of last heartbeat update
}

// defaultFileSystem implements FileSystem using the standard os package
type defaultFileSystem struct{}

func (fs *defaultFileSystem) ReadLockFile(filePath string) (LockInfo, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return LockInfo{}, err
	}

	var lockInfo LockInfo
	if err := json.Unmarshal(data, &lockInfo); err != nil {
		// Corrupted JSON is treated as no lock file
		return LockInfo{}, os.ErrNotExist
	}

	return lockInfo, nil
}

func (fs *defaultFileSystem) WriteLockFile(filePath string, lockInfo LockInfo) error {
	data, err := json.Marshal(lockInfo)
	if err != nil {
		return err
	}

	return os.WriteFile(filePath, data, 0644)
}

func (fs *defaultFileSystem) RemoveLockFile(filePath string) error {
	err := os.Remove(filePath)
	if os.IsNotExist(err) {
		return nil // Already removed, no error
	}
	return err
}

// defaultSystemClock implements SystemClock using the standard time package
type defaultSystemClock struct{}

func (c *defaultSystemClock) Now() time.Time {
	return time.Now()
}

// defaultProcessManager implements ProcessManager using the standard os package
type defaultProcessManager struct{}

func (pm *defaultProcessManager) GetPID() int {
	return os.Getpid()
}

func (pm *defaultProcessManager) Exists(pid int) bool {
	if pid <= 0 {
		return false
	}

	// Check if process exists by sending signal 0
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	err = process.Signal(syscall.Signal(0))
	return err == nil
}

func (pm *defaultProcessManager) Kill(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid PID: %d", pid)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}

	return process.Signal(syscall.SIGKILL)
}

// getOrCreateDefaults returns default implementations for nil dependencies
func getOrCreateDefaults(deps *Dependencies) (FileSystem, SystemClock, ProcessManager) {
	var fs FileSystem
	var clock SystemClock
	var pm ProcessManager

	if deps != nil {
		fs = deps.FileSystem
		clock = deps.Clock
		pm = deps.ProcessManager
	}

	if fs == nil {
		fs = &defaultFileSystem{}
	}
	if clock == nil {
		clock = &defaultSystemClock{}
	}
	if pm == nil {
		pm = &defaultProcessManager{}
	}

	return fs, clock, pm
}

// setDefaultOptions sets default values for unspecified options
func setDefaultOptions(options *Options) {
	if options.StaleTimeout == 0 {
		options.StaleTimeout = time.Hour
	}
	if options.HeartBeatMinimalInterval == 0 {
		options.HeartBeatMinimalInterval = time.Minute
	}
}

// Acquire attempts to acquire a lock at the specified file path.
//
// If successful, it returns a Lock instance that must be used to maintain
// the lock through heartbeat updates and eventually release it.
//
// The function will:
//   - Create a new lock if no lock file exists
//   - Allow re-acquisition if the same process already holds the lock
//   - Take over stale locks (older than StaleTimeout)
//   - Return ErrLockBusy if another active process holds the lock
//
// Parameters:
//   - filePath: Path where the lock file will be created
//   - options: Configuration options (zero values use defaults)
//
// Returns:
//   - *Lock: Lock instance for maintaining and releasing the lock
//   - error: ErrLockBusy if lock is held by another process, other errors for failures
func Acquire(filePath string, options Options) (*Lock, error) {
	setDefaultOptions(&options)
	fs, clock, pm := getOrCreateDefaults(options.Dependencies)

	currentPID := pm.GetPID()
	now := clock.Now()

	// Try to read existing lock file
	existingLock, err := fs.ReadLockFile(filePath)
	if err == nil {
		// Lock file exists, check if it's stale or from same process
		if existingLock.ProcessID == currentPID {
			// Same process can re-acquire its own lock
			return &Lock{
				filePath:      filePath,
				options:       options,
				fs:            fs,
				clock:         clock,
				pm:            pm,
				released:      false,
				lastHeartBeat: now,
			}, nil
		}

		// Check if lock is stale
		if now.Sub(existingLock.Timestamp) > options.StaleTimeout {
			// Lock is stale, try to clean up the process
			if pm.Exists(existingLock.ProcessID) {
				// Process still exists, send SIGKILL to terminate it
				if err := pm.Kill(existingLock.ProcessID); err != nil {
					return nil, fmt.Errorf("failed to kill stale process %d: %w", existingLock.ProcessID, err)
				}
			}
			// Process doesn't exist or was killed, proceed to acquire lock
		} else {
			// Lock is still active
			return nil, ErrLockBusy
		}
	} else if !os.IsNotExist(err) {
		// Some other error reading the file
		return nil, fmt.Errorf("failed to read lock file: %w", err)
	}

	// Create new lock
	lockInfo := LockInfo{
		ProcessID: currentPID,
		Timestamp: now,
	}

	if err := fs.WriteLockFile(filePath, lockInfo); err != nil {
		return nil, fmt.Errorf("failed to write lock file: %w", err)
	}

	return &Lock{
		filePath:      filePath,
		options:       options,
		fs:            fs,
		clock:         clock,
		pm:            pm,
		released:      false,
		lastHeartBeat: now,
	}, nil
}

// HeartBeat signals that the process is still alive and updates the lock file
// timestamp if the minimal interval has elapsed.
//
// This method should be called regularly by long-running processes to prove
// they are still active and prevent other processes from considering the
// lock stale. The frequency of updates is controlled by HeartBeatMinimalInterval.
//
// The method is safe for concurrent use and will:
//   - Update the lock file timestamp if enough time has passed
//   - Verify the process still owns the lock
//   - Panic if the lock file disappears or is hijacked by another process
//   - Silently return if called after Release()
//
// Returns:
//   - nil: Heartbeat successful or not needed yet
//   - error: Failed to update lock file (excluding panic conditions)
func (l *Lock) HeartBeat() error {
	now := l.clock.Now()

	// Check if we need to update based on interval (without lock for performance)
	if l.options.HeartBeatMinimalInterval > 0 && now.Sub(l.lastHeartBeat) < l.options.HeartBeatMinimalInterval {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Silently ignore heartbeat calls after release
	if l.released {
		return nil
	}

	// Read current lock file to verify we still own it
	currentLock, err := l.fs.ReadLockFile(l.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			panic("lock file disappeared")
		}
		return fmt.Errorf("failed to read lock file during heartbeat: %w", err)
	}

	// Verify we still own the lock
	if currentLock.ProcessID != l.pm.GetPID() {
		panic("lock file hijacked by another process")
	}

	// Update timestamp
	lockInfo := LockInfo{
		ProcessID: l.pm.GetPID(),
		Timestamp: now,
	}

	if err := l.fs.WriteLockFile(l.filePath, lockInfo); err != nil {
		return fmt.Errorf("failed to update lock file during heartbeat: %w", err)
	}

	l.lastHeartBeat = now
	return nil
}

// Release releases the lock and removes the lock file.
//
// This method should be called when the process no longer needs the lock,
// typically in a defer statement after successful acquisition.
// Multiple calls to Release() are safe and will not return an error.
//
// After Release() is called:
//   - The lock file is removed from the filesystem
//   - Subsequent HeartBeat() calls are silently ignored
//   - The Lock instance should not be used for further operations
//
// Returns:
//   - nil: Lock successfully released or already released
//   - error: Failed to remove lock file
func (l *Lock) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Multiple releases are safe
	if l.released {
		return nil
	}

	l.released = true

	// Remove the lock file
	if err := l.fs.RemoveLockFile(l.filePath); err != nil {
		return fmt.Errorf("failed to remove lock file: %w", err)
	}

	return nil
}
