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

// ErrLockBusy is returned when a lock is held by another active process
var ErrLockBusy = errors.New("lock is held by another process")

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
	StaleTimeout            time.Duration // default: 1 hour
	HeartBeatUpdateInterval time.Duration // default: 1 minute
	Dependencies            *Dependencies // optional for testing
}

// Lock represents an acquired lock
type Lock struct {
	filePath      string
	options       Options
	fs            FileSystem
	clock         SystemClock
	pm            ProcessManager
	mu            sync.Mutex
	released      bool
	lastHeartBeat time.Time
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
	if options.HeartBeatUpdateInterval == 0 {
		options.HeartBeatUpdateInterval = time.Minute
	}
}

// Acquire attempts to acquire a lock at the specified file path
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

// HeartBeat signals that the process is still alive and updates the lock file if needed
func (l *Lock) HeartBeat() error {
	now := l.clock.Now()

	// Check if we need to update based on interval (without lock for performance)
	if l.options.HeartBeatUpdateInterval > 0 && now.Sub(l.lastHeartBeat) < l.options.HeartBeatUpdateInterval {
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

// Release releases the lock and removes the lock file
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