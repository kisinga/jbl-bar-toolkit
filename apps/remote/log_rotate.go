package main

import (
	"log"
	"os"
	"path/filepath"
	"syscall"
)

// logRotateMax caps remote.log. start.sh points the remote's standard error at
// remote.log and only moves it aside at the next start, so a path that logs
// on every poll could fill the bar's small writable storage. Past this size,
// remote.log becomes remote.log.1 (dropping the one before) and a new
// remote.log starts: at most twice this on disk for the running remote.
const logRotateMax = 512 << 10

// rotatingLog is the log package's output once the remote runs as a service.
// log.Logger serializes its writes, so Write is never called concurrently.
type rotatingLog struct {
	path string
	max  int64
	file *os.File // where the log goes now
	size int64
	// fds are pointed at each new file, so that what is written to them
	// directly (a crash report on standard error) lands in the current log.
	fds []int
}

// startLogRotation caps remote.log when standard error is a regular file (the
// service started by start.sh); in a terminal or with no file, it does nothing.
func startLogRotation() {
	path, err := os.Readlink("/proc/self/fd/2")
	if err != nil || !filepath.IsAbs(path) {
		return
	}
	info, err := os.Stderr.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	log.SetOutput(&rotatingLog{path: path, max: logRotateMax, file: os.Stderr, size: info.Size(), fds: []int{1, 2}})
}

func (l *rotatingLog) Write(p []byte) (int, error) {
	if l.size > 0 && l.size+int64(len(p)) > l.max {
		l.rotate()
	}
	n, err := l.file.Write(p)
	l.size += int64(n)
	return n, err
}

// rotate moves the log to path.1 and goes on in a new file. If that cannot be
// done, it empties the current file instead: the cap holds either way.
func (l *rotatingLog) rotate() {
	if os.Rename(l.path, l.path+".1") == nil {
		if f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|os.O_APPEND, 0600); err == nil {
			for _, fd := range l.fds {
				_ = syscall.Dup3(int(f.Fd()), fd, 0)
			}
			if l.file != os.Stderr {
				l.file.Close()
			}
			l.file, l.size = f, 0
			return
		}
	}
	if l.file.Truncate(0) == nil {
		_, _ = l.file.Seek(0, 0)
		l.size = 0
	}
}
