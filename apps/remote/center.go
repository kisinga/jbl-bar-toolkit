package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Center channel level.
//
// The firmware has no native center setting, so this control writes the
// per-channel digital gain of the TAS5825M amplifier that drives the center
// pair (8-bit address 0x98: left = center tweeter, right = center driver).
// It uses the same proprietary /dev/i2c descriptor and book/page sequence as
// bdpprog's own amplifier code (tas5825_channel_test / pb_select), on the
// hash-pinned firmware only. Level 0 is the factory 0 dB coefficient; until a
// different level is chosen, nothing is ever written to the amplifiers.
//
// The amplifiers reload their defaults when the bar wakes, so a non-default
// level is re-applied after each wake or firmware restart. Readback through
// the read ioctl is not available on the qualified unit, so the reported
// level is the requested one.

const (
	centerDevice       = "/dev/i2c"
	centerIoctlWrite   = 0x40047301
	centerAmp          = 0x98
	centerGainBook     = 0x8C
	centerGainPage     = 0x0B
	centerRegLeft      = 0x0C
	centerRegRight     = 0x10
	centerLevels       = 6
	centerDefault      = 0 // level 0: original 0 dB, never written
	centerLevelPath    = "/var/JBLRemote/center-level"
	centerWakeSettle   = 4 * time.Second
	centerWakeConfirm  = 15 * time.Second
	centerDescriptorSz = 36
)

// Q9.23 big-endian coefficients: round(10^(dB/20) * 2^23) for
// 0, +2.4, +4.8, +7.2, +9.6 and +12 dB. Level 0 is the firmware's own 0 dB value;
// the control only boosts, never cuts below the original tuning.
var centerCoefficients = [centerLevels]uint32{0x00800000, 0x00A8BCA3, 0x00DE7042, 0x01253B27, 0x01828DC7, 0x01FD93C2}
var centerDB = [centerLevels]float64{0, 2.4, 4.8, 7.2, 9.6, 12}

type centerController struct {
	level        uint32
	loaded       bool
	wasOn        bool
	wakeAt       time.Time
	passes       int
	appliedStart string
	problem      string
}

// centerIoctl is replaced in host tests; on the bar it is the raw syscall.
var centerIoctl = func(fd uintptr, request uintptr, arg unsafe.Pointer) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(arg)); errno != 0 {
		return errno
	}
	return nil
}

var centerOpen = func() (*os.File, error) { return os.OpenFile(centerDevice, os.O_RDWR, 0) }

// centerDescriptor lays out bdpprog's 36-byte amplifier transfer descriptor.
func centerDescriptor(reg uint16, data []byte) [centerDescriptorSz]byte {
	var d [centerDescriptorSz]byte
	d[0] = centerAmp >> 1
	binary.LittleEndian.PutUint16(d[2:], reg)
	binary.LittleEndian.PutUint16(d[4:], uint16(len(data)))
	binary.LittleEndian.PutUint32(d[8:], uint32(uintptr(unsafe.Pointer(&data[0]))))
	binary.LittleEndian.PutUint32(d[0x18:], 0x32)
	d[0x1c], d[0x1d] = 0x63, 0x62
	binary.LittleEndian.PutUint32(d[0x20:], 1)
	return d
}

// centerWrite is replaced in host tests to record the register sequence.
var centerWrite = centerWriteIoctl

func centerWriteIoctl(f *os.File, reg uint16, data []byte) error {
	buf := append([]byte(nil), data...)
	d := centerDescriptor(reg, buf)
	e := centerIoctl(f.Fd(), centerIoctlWrite, unsafe.Pointer(&d[0]))
	runtime.KeepAlive(buf)
	runtime.KeepAlive(&d)
	return e
}

func centerSelect(f *os.File, book, page byte) error {
	for _, step := range [][2]byte{{0x00, 0x00}, {0x7F, book}, {0x00, page}} {
		if e := centerWrite(f, uint16(step[0]), []byte{step[1]}); e != nil {
			return e
		}
	}
	return nil
}

// writeCenterGain sets both center channels and always returns the
// amplifier to book 0 / page 0, where the firmware expects it.
func writeCenterGain(level uint32) error {
	if level >= centerLevels {
		return errors.New("center level out of range")
	}
	f, e := centerOpen()
	if e != nil {
		return e
	}
	defer f.Close()
	var coeff [4]byte
	binary.BigEndian.PutUint32(coeff[:], centerCoefficients[level])
	e = centerSelect(f, centerGainBook, centerGainPage)
	if e == nil {
		e = centerWrite(f, centerRegLeft, coeff[:])
	}
	if e == nil {
		e = centerWrite(f, centerRegRight, coeff[:])
	}
	if r := centerSelect(f, 0, 0); e == nil {
		e = r
	}
	return e
}

func loadCenterLevel(path string) (uint32, error) {
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return centerDefault, nil
	}
	if e != nil {
		return centerDefault, e
	}
	v, e := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 32)
	if e != nil || v >= centerLevels {
		return centerDefault, errors.New("invalid saved center level")
	}
	return uint32(v), nil
}

func saveCenterLevel(path string, level uint32) error {
	if level == centerDefault {
		if e := os.Remove(path); e != nil && !os.IsNotExist(e) {
			return e
		}
		return nil
	}
	tmp := filepath.Join(filepath.Dir(path), ".center-level.new")
	if e := os.WriteFile(tmp, []byte(strconv.Itoa(int(level))+"\n"), 0600); e != nil {
		return e
	}
	return os.Rename(tmp, path)
}

func (d *device) centerLevel() uint32 {
	c := &d.center
	if !c.loaded {
		v, e := loadCenterLevel(centerLevelPath)
		c.level, c.loaded = v, true
		if e != nil {
			c.problem = e.Error()
		}
	}
	return c.level
}

func (d *device) powerOn() bool {
	p, ep := d.read(14191893, 1)
	system, es := d.read(12962676, 4)
	return ep == nil && es == nil && p == 1 && system == 0
}

// centerAction advances the center level like the native bass control.
func (d *device) centerAction(ctx context.Context, action string) (state, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if action != "center_next" {
		return d.snapshotCurrent(), errors.New("unknown control")
	}
	if e := d.identify(); e != nil {
		return d.snapshotCurrent(), e
	}
	before := d.snapshotLocked()
	if !before.Available {
		return before, errors.New(before.Error)
	}
	if before.Power != "On" {
		return before, errors.New("turn the bar on first")
	}
	if !d.idleKeys() || controlBusy() {
		return before, errors.New("remote control is busy; try again shortly")
	}
	if e := ctx.Err(); e != nil {
		return before, e
	}
	next := (d.centerLevel() + 1) % centerLevels
	if e := writeCenterGain(next); e != nil {
		return d.snapshotCurrent(), errors.New("center level not applied: " + e.Error())
	}
	c := &d.center
	c.level, c.problem = next, ""
	c.markApplied(d.start, time.Now())
	if e := saveCenterLevel(centerLevelPath, next); e != nil {
		c.problem = "center level applied but not saved: " + e.Error()
		return d.snapshotCurrent(), errors.New(c.problem)
	}
	return d.snapshotCurrent(), nil
}

// centerDue decides whether a saved non-default level must be re-applied:
// once shortly after wake (or a firmware restart), then once more to cover
// a late amplifier initialisation. Writes are idempotent.
func (c *centerController) centerDue(on bool, start string, now time.Time) bool {
	if !on {
		c.wasOn = false
		return false
	}
	// Track wakes at every level, so that a level chosen just after a wake
	// still gets the remaining passes.
	if !c.wasOn || c.appliedStart != start {
		c.wasOn, c.wakeAt, c.passes, c.appliedStart = true, now, 0, start
	}
	if c.level == centerDefault {
		return false
	}
	switch {
	case c.passes == 0 && now.Sub(c.wakeAt) >= centerWakeSettle:
		return true
	case c.passes == 1 && now.Sub(c.wakeAt) >= centerWakeConfirm:
		return true
	}
	return false
}

// markApplied records a level written by the control. The amplifiers can
// still reload their defaults shortly after a wake, so the wake passes are
// kept (or started, when the monitor has not seen the wake yet) and re-apply
// the new level; only a change long after the wake skips them.
func (c *centerController) markApplied(start string, now time.Time) {
	switch {
	case !c.wasOn || c.appliedStart != start:
		c.wasOn, c.wakeAt, c.passes, c.appliedStart = true, now, 0, start
	case now.Sub(c.wakeAt) >= centerWakeConfirm:
		c.passes = 2
	}
}

func (r *remote) monitorCenter(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		release, _, held := r.hold("")
		if !held {
			continue
		}
		d := r.device
		d.mu.Lock()
		if d.identify() == nil {
			d.centerLevel()
			c := &d.center
			if c.centerDue(d.powerOn(), d.start, time.Now()) && d.idleKeys() && !controlBusy() {
				if e := writeCenterGain(c.level); e != nil {
					c.problem = "center level not re-applied: " + e.Error()
				} else {
					c.passes++
					c.problem = ""
				}
			}
		}
		d.mu.Unlock()
		release()
	}
}

// centerDisplayID: one message for the center level, so that steps replace
// each other and the last level shows.
const centerDisplayID = "center-level"

// centerDisplayText is how the front display shows a center level, numbered
// 1 to 6 like the page.
func centerDisplayText(level uint32) string { return fmt.Sprintf("CENTER %d", level+1) }

// showCenterLevel shows the level just applied on the front display, as the
// bar does for its own bass level: the center level is not native, so the bar
// shows nothing by itself. Without display messages, nothing is shown.
func showCenterLevel(d *displayManager, level uint32) {
	if d == nil {
		return
	}
	if err := d.Show(displayRequest{ID: centerDisplayID, Text: centerDisplayText(level), Mode: displayPasses, Passes: 1}); err != nil {
		displayLog("Center level %d: not shown: %v", level+1, err)
	}
}
