package main

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openLog(t *testing.T, path string) *os.File {
	t.Helper()
	// As start.sh's redirection opens it: truncated, not in append mode.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestLogRotatesPastItsCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote.log")
	f := openLog(t, path)
	l := &rotatingLog{path: path, max: 1000, file: f}
	logger := log.New(l, "", 0)
	line := strings.Repeat("x", 99) // 100 bytes with the newline
	for i := range 35 {
		logger.Printf("%02d%s", i, line[2:])
	}
	current, _ := os.ReadFile(path)
	previous, _ := os.ReadFile(path + ".1")
	if len(current) > 1000 || len(previous) > 1000 {
		t.Fatalf("over the cap: remote.log %d bytes, remote.log.1 %d bytes", len(current), len(previous))
	}
	// The newest lines are in remote.log, the ones just before in remote.log.1.
	if !bytes.HasPrefix(current, []byte("30")) || !bytes.HasSuffix(bytes.TrimSpace(current), []byte(line[2:])) || !bytes.HasPrefix(previous, []byte("20")) {
		t.Fatalf("remote.log starts %q, remote.log.1 starts %q", current[:2], previous[:2])
	}
	if strings.Count(string(current), "\n") != 5 || strings.Count(string(previous), "\n") != 10 {
		t.Fatalf("lines lost: %d in remote.log, %d in remote.log.1", strings.Count(string(current), "\n"), strings.Count(string(previous), "\n"))
	}
}

// What is written straight to the file descriptors (a crash report on standard
// error) follows the log into the new file.
func TestLogRotationMovesTheFileDescriptors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "remote.log")
	f := openLog(t, path)
	spare := openLog(t, filepath.Join(dir, "spare")) // stands for standard error
	l := &rotatingLog{path: path, max: 10, file: f, fds: []int{int(spare.Fd())}}
	l.Write([]byte("0123456789\n"))
	l.Write([]byte("after\n"))
	if _, err := spare.Write([]byte("panic\n")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "after\npanic\n" {
		t.Fatalf("remote.log: %q", b)
	}
}

// If the log cannot be moved aside, it is emptied: the cap holds anyway.
func TestLogEmptiedWhenItCannotRotate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "remote.log")
	f := openLog(t, path)
	if err := os.Mkdir(path+".1", 0700); err != nil { // the rename onto a directory fails
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path+".1", "keep"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	l := &rotatingLog{path: path, max: 10, file: f}
	l.Write([]byte("0123456789\n"))
	l.Write([]byte("after\n"))
	if b, _ := os.ReadFile(path); string(b) != "after\n" {
		t.Fatalf("remote.log: %q", b)
	}
}
