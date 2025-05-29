# livinglock

**English** | [日本語](README.ja.md)

A robust file-based process locking mechanism specifically designed for **scheduled batch processing** in Go. Unlike traditional PID locks, `livinglock` detects deadlocked processes through active heartbeat monitoring.

## Problem Solved

Traditional batch job exclusion methods fail in real-world scenarios:

- **Simple PID locks**: Cannot detect processes stuck in deadlocks or infinite loops
- **Time-based locks**: Kill healthy long-running processes prematurely  
- **No locks**: Allow harmful concurrent executions

`livinglock` requires **active proof-of-work signals** from the lock holder, ensuring not just process existence but actual ongoing work.

## Quick Start

### Installation

```bash
go get github.com/ideamans/living-lock
```

### Basic Usage

```go
package main

import (
    "errors"
    "log"
    "time"
    
    "github.com/ideamans/living-lock"
)

func main() {
    // Acquire lock for batch processing
    lock, err := livinglock.Acquire("/tmp/daily-batch.lock", livinglock.Options{
        StaleTimeout:             2 * time.Hour,  // Max expected runtime
        HeartBeatMinimalInterval: 5 * time.Minute, // Heartbeat frequency
    })
    if err != nil {
        if errors.Is(err, livinglock.ErrLockBusy) {
            log.Printf("Previous batch still running, yielding priority")
            return // Gracefully exit
        }
        log.Fatalf("Failed to acquire lock: %v", err)
    }
    defer lock.Release()

    log.Printf("Starting batch processing...")
    
    // Process work with periodic heartbeats
    for i := 0; i < 10; i++ {
        // Do actual work
        processDataBatch(i)
        
        // Signal that we're actively working (not deadlocked)
        if err := lock.HeartBeat(); err != nil {
            log.Printf("Heartbeat failed: %v", err)
            return
        }
        
        log.Printf("Completed batch %d", i)
    }
    
    log.Printf("Batch processing completed")
}

func processDataBatch(id int) {
    // Simulate work
    time.Sleep(30 * time.Second)
}
```

## How It Works

### Scenarios Handled

1. **Normal case**: New cron job starts, previous job finished → runs normally
2. **Active process**: Previous job still working with heartbeats → new job yields gracefully  
3. **Deadlocked process**: Previous job stuck without heartbeats → new job takes over
4. **Crashed process**: Previous job terminated → new job acquires lock immediately

### Lock States

```bash
# Healthy: Process exists AND sends heartbeats → don't interfere
# Deadlocked: Process exists BUT no heartbeats → safe to take over
# Crashed: Process doesn't exist → safe to take over
```

## API Reference

### `Acquire(filePath string, options Options) (*Lock, error)`

Attempts to acquire a lock at the specified file path.

**Returns:**
- `*Lock`: Lock instance if successful
- `ErrLockBusy`: Another active process holds the lock
- `error`: Unexpected errors (file permissions, disk space, etc.)

### `Lock.HeartBeat() error`

Sends a heartbeat signal proving the process is actively working. Call this periodically during long-running operations.

### `Lock.Release() error`

Releases the lock and removes the lock file. Safe to call multiple times.

### Options

```go
type Options struct {
    StaleTimeout             time.Duration // When to consider a lock stale (default: 1 hour)
    HeartBeatMinimalInterval time.Duration // Minimum interval between heartbeats (default: 1 minute)
    Dependencies             *Dependencies // For testing (optional)
}
```

## Best Practices

### Cron Jobs

```go
// In your cron-scheduled batch job
func main() {
    lock, err := livinglock.Acquire("/var/run/backup.lock", livinglock.Options{
        StaleTimeout: 4 * time.Hour, // Max backup time
    })
    if err != nil {
        if errors.Is(err, livinglock.ErrLockBusy) {
            return // Previous backup still running
        }
        log.Fatal(err)
    }
    defer lock.Release()
    
    // Your backup logic with periodic HeartBeat() calls
}
```

### Heartbeat Frequency

- Call `HeartBeat()` at regular intervals during work
- Frequency should be much less than `StaleTimeout`
- Consider your work's natural checkpoint intervals

### Error Handling

- `ErrLockBusy`: Expected behavior, handle gracefully
- Other errors: Typically indicate system issues (permissions, disk space)

## Testing

The package includes comprehensive tests including integration tests with real multiprocess scenarios:

```bash
go test ./...
```

## License

MIT License - see [LICENSE](LICENSE) file for details.

## Contributing

Contributions are welcome! Please read the contributing guidelines and ensure all tests pass.