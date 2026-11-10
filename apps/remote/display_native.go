package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// monotonicMS is CLOCK_MONOTONIC in ms, the clock bdpprog's display tick uses
// (0x253434 calls clock_gettime(CLOCK_MONOTONIC)).
func monotonicMS() (int64, error) {
	var ts syscall.Timespec
	if _, _, errno := syscall.Syscall(syscall.SYS_CLOCK_GETTIME, 1, uintptr(unsafe.Pointer(&ts)), 0); errno != 0 {
		return 0, errno
	}
	return ts.Nano() / int64(time.Millisecond), nil
}

// barDisplayPort reaches bdpprog's display fields under the shared action gate.
type barDisplayPort struct{ r *remote }

func (p barDisplayPort) now() time.Time { return time.Now() }

func (p barDisplayPort) sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// step runs f under the action gate (unreported, like the monitors) and the
// device lock. force skips the gate: only for handing the display back.
func (p barDisplayPort) step(force bool, f func(displayMem) error) error {
	if !force {
		release, _, ok := p.r.hold("")
		if !ok {
			return errDisplayGate
		}
		defer release()
	}
	d := p.r.device
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pid == 0 || !d.same() {
		if e := d.identify(); e != nil {
			return e
		}
	}
	return f(barDisplayMem{d})
}

type barDisplayMem struct{ d *device }

func (m barDisplayMem) read32(address int64) (uint32, error) { return m.d.read(address, 4) }

// writeDisplay writes only the four display fields the custom message uses.
func (m barDisplayMem) writeDisplay(address int64, b []byte) error {
	switch {
	case (address == vfdLevel2 || address == vfdLevel3 || address == vfdTempTick) && len(b) == 4:
	case address == vfdTempText && len(b) == vfdTextSize:
	default:
		return errors.New("write outside display message fields")
	}
	if !m.d.same() {
		return errors.New("soundbar restarted")
	}
	f, e := os.OpenFile(fmt.Sprintf("/proc/%d/mem", m.d.pid), os.O_WRONLY, 0)
	if e != nil {
		return e
	}
	defer f.Close()
	_, e = f.WriteAt(b, address)
	return e
}

func (m barDisplayMem) keyDown() (bool, error) {
	v, e := m.d.read(vfdKeyFlag, 1)
	return v != 0, e
}

func (m barDisplayMem) write32(address int64, value uint32) error {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], value)
	return m.writeDisplay(address, b[:])
}

func (m barDisplayMem) writeText(text string) error {
	if len(text) > displayTextMax {
		return errors.New("display text too long")
	}
	b := make([]byte, vfdTextSize) // NUL-terminated, zero padded like the native consumer
	copy(b, text)
	return m.writeDisplay(vfdTempText, b)
}

func (m barDisplayMem) tick() (uint32, error) {
	period, e := m.d.read(vfdTickPeriod, 4)
	if e != nil {
		return 0, e
	}
	if period == 0 || period > 1000 {
		return 0, errors.New("display tick period unreadable")
	}
	ms, e := monotonicMS()
	if e != nil {
		return 0, e
	}
	return uint32(ms / int64(period)), nil
}

func displayLog(format string, args ...any) { log.Printf(format, args...) }
