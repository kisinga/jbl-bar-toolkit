package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"syscall"
)

// Front EQ kept until Reset.
//
// Device evidence (one Bar 9.1, firmware 23.06.41.80, 4 Nov 2026, read-only
// probe of the native block; see models/bar91/validation/front-eq-keep-device.json):
//   - Bass, Atmos and Rear keys: in custom mode 5 the firmware recomputes the
//     block for the new level itself. The result is byte-identical to the same
//     EQ applied directly at that level, and returning to the first level gives
//     the first EQ block back.
//   - Night mode on/off: mode 5 and the block are unchanged.
//   - Source change (Bluetooth -> HDMI -> Bluetooth) and Standard -> Smart ->
//     Standard: the firmware drops the EQ and returns to the native preset
//     (mode 0, original block); Smart itself reads mode 1, cache 1.
//   - Without EQ, the block depends only on the levels.
//   - Applying takes 2.1-2.3 s and removing 3.3-4.1 s, muted meanwhile.
//
// So these controls no longer remove the EQ first. The monitor accepts a block
// the firmware recomputed after a level change, and re-applies the chosen EQ
// once the firmware has dropped it and the qualified route is back. The chosen
// EQ is kept in eqKeepPath across standby and restarts; Reset forgets it.

var eqKeepPath = "/var/JBLRemote/front-eq.json"

// eqKeepStable monitor ticks (two seconds each) of an idle, qualified route
// with the native preset before the kept EQ is applied again.
const eqKeepStable = 3

const eqKeepWaiting = "Your EQ is re-applied automatically when Standard audio plays."

// eqFollowsNatively reports native controls the firmware handles with an EQ
// applied: it keeps (night) or recomputes (levels) the EQ, or drops it itself
// (input, Smart/Standard), so removing it first only adds a muted reset.
func eqFollowsNatively(action string) bool {
	switch action {
	case "bass_next", "atmos_next", "rear_next", "night_toggle", "standard", "smart",
		"input_tv", "input_hdmi", "input_bluetooth":
		return true
	}
	return false
}

// eqLevels are the native settings the EQ block is built from.
type eqLevels struct {
	Bass  uint32 `json:"bass"`
	Atmos uint32 `json:"atmos"`
	Rear  uint32 `json:"rear"`
}

func (d *device) eqLevels() (eqLevels, bool) {
	b, e1 := d.configByte(238)
	a, e2 := d.configByte(201)
	r, e3 := d.configByte(202)
	if e1 != nil || e2 != nil || e3 != nil || b > 4 || a > 2 || r > 3 {
		return eqLevels{}, false
	}
	return eqLevels{b, a, r}, true
}

// eqFollow is what the monitor does with an applied EQ.
type eqFollow int

const (
	eqFollowOK      eqFollow = iota // the block is the one expected
	eqFollowLevels                  // the firmware recomputed it for new levels
	eqFollowDropped                 // the firmware returned to the native preset
	eqFollowSmart                   // Smart Mode: wait for Standard
	eqFollowReset                   // anything else: reset, as before
)

// eqFollowDecision is pure: n/readErr is the native read, smart whether the bar
// is in Smart Mode, levelsChanged whether bass/Atmos/rear differ from the
// levels the expected block was made for.
func eqFollowDecision(n eqNativeState, readErr error, native eqNativeState, expected []byte, smart, levelsChanged bool) eqFollow {
	if smart {
		return eqFollowSmart
	}
	if readErr != nil {
		return eqFollowReset
	}
	if n.Mode == 0 && n.Cache == 0 {
		return eqFollowDropped
	}
	if n.Mode != 5 || n.outer != native.outer || n.SampleRate != native.SampleRate {
		return eqFollowReset
	}
	if bytes.Equal(n.Block, expected) {
		return eqFollowOK
	}
	if levelsChanged {
		return eqFollowLevels
	}
	return eqFollowReset
}

func (d *device) smartModeNow() bool {
	v, e := d.read(13378532, 4)
	return e == nil && v == 3
}

// followFrontEQ handles an applied EQ for one monitor tick. Callers hold d.mu.
// report names a reset while it runs (seconds, muted), so clients wait for
// it as for any control instead of seeing an unexplained refusal.
func (d *device) followFrontEQ(ctx context.Context, report func(string)) {
	m := &d.eq
	n, readErr := d.readEQNative()
	levels, known := d.eqLevels()
	changed := known && m.levelsKnown && levels != m.levels
	decision := eqFollowDecision(n, readErr, m.native, m.expected, d.smartModeNow(), changed)
	// A reset that failed is retried only once the native state changes:
	// retrying it every tick reported a control every two seconds, and the
	// page flashed "Another control is running" with its controls disabled.
	key := eqFollowKey(n, readErr)
	if !eqFollowRetry(decision, m.followBlocked, key) {
		return
	}
	// Turned off while it could not be reset: finish once the route allows it.
	if m.offPending && decision != eqFollowSmart && readErr == nil {
		report("eq_reset")
		if e := d.resetFrontEQ(ctx); e != nil {
			m.problem, m.followBlocked = eqPendingProblem(e, d.readHPPMode), key
			m.note("Front EQ turn-off pending: %v", e)
			return
		}
		m.note("Front EQ turned off: original tuning restored")
		return
	}
	switch decision {
	case eqFollowOK:
		return
	case eqFollowLevels:
		// The firmware applied our recipe to the new levels itself. The saved
		// baseline belongs to the old levels; a later reset or new EQ starts
		// from the native preset instead (see resetFrontEQ, applyFrontEQ).
		m.expected = append([]byte(nil), n.Block...)
		m.levels, m.baselineStale = levels, true
		m.note("Front EQ follows the new sound levels (bass %d of 5, Atmos %d, rear %d)", levels.Bass+1, levels.Atmos, levels.Rear)
	case eqFollowSmart:
		m.note("Front EQ waits for Standard Mode")
	case eqFollowDropped:
		// Source change, Standard after Smart, wake: the native preset is
		// already back. Finish the reset without muting; the keeper applies
		// the chosen EQ again.
		report("eq_reset")
		if e := d.resetFrontEQ(ctx); e != nil {
			m.problem, m.followBlocked = eqPendingProblem(e, d.readHPPMode), key
			m.note("Front EQ cleanup pending: %v", e)
			return
		}
		m.note("Front EQ removed by the bar; it is applied again when Standard audio plays")
	default:
		report("eq_reset")
		if e := d.resetFrontEQ(ctx); e != nil {
			m.problem, m.followBlocked = eqPendingProblem(e, d.readHPPMode), key
			m.note("Front EQ reset pending: %v", e)
		}
	}
}

// eqDeferFor is why a new EQ cannot be applied now but can be kept for
// later, or "" when it must be applied (or refused) now. Problems, a reset
// pending on an applied EQ and unreadable states are never deferred.
func eqDeferFor(problem string, available bool, power, mode string, active bool, routeErr error) string {
	switch {
	case problem != "" || !available || active:
		return ""
	case power != "On":
		return "standby"
	case mode != "Standard":
		return "Smart Mode"
	case errors.Is(routeErr, errEQRoute):
		return "this audio route"
	}
	return ""
}

// eqDeferReason applies eqDeferFor to the bar now. Callers hold d.mu.
func (d *device) eqDeferReason() string {
	m := &d.eq
	if m.problem != "" || m.active || eqParserPayload == "" {
		return ""
	}
	s := d.snapshotCurrent()
	var routeErr error
	if s.Available && s.Power == "On" && s.Mode == "Standard" {
		_, routeErr = d.readEQNative()
	}
	return eqDeferFor(m.problem, s.Available, s.Power, s.Mode, m.active, routeErr)
}

// eqBlockDiff counts the bytes that differ between two native blocks and
// reports whether they all lie in the bass offset gain (0xdac..0xdb0).
func eqBlockDiff(a, b []byte) (count int, offsetOnly bool) {
	if len(a) != len(b) {
		return max(len(a), len(b)), false
	}
	offsetOnly = true
	for i := range a {
		if a[i] != b[i] {
			count++
			if i < 0xdac || i >= 0xdb0 {
				offsetOnly = false
			}
		}
	}
	return count, offsetOnly && count > 0
}

// turnOffFrontEQ removes the EQ now when the bar allows it. In standby, Smart
// Mode or on another route it cannot be reset yet: it is marked off and the
// monitor removes it once Standard audio plays, so turning off never fails
// for "not now". Callers hold d.mu and have forgotten the kept EQ.
func (d *device) turnOffFrontEQ(ctx context.Context) error {
	m := &d.eq
	if !m.recoveryChecked {
		d.recoverFrontEQ(ctx)
	}
	if !m.active && !m.needsReset {
		return nil
	}
	s := d.snapshotCurrent()
	if s.Available && s.Power == "On" && s.Mode == "Standard" {
		e := d.resetFrontEQ(ctx)
		if e == nil || !errors.Is(e, errEQRoute) {
			return e
		}
	}
	m.offPending = true
	m.note("Front EQ turned off; the bar returns to its original tuning once Standard audio plays")
	return nil
}

// eqFollowRetry reports whether the monitor acts on decision: a reset is not
// tried again on the native state where it last failed.
func eqFollowRetry(decision eqFollow, blocked, key string) bool {
	return (decision != eqFollowDropped && decision != eqFollowReset) || blocked == "" || blocked != key
}

// eqFollowKey identifies the native state a reset decision was made on.
func eqFollowKey(n eqNativeState, readErr error) string {
	if readErr != nil {
		return "error: " + readErr.Error()
	}
	return fmt.Sprintf("mode %d cache %d element %#x rate %d %s", n.Mode, n.Cache, n.Element, n.SampleRate, eqDigest(n.Block))
}

// keepFrontEQ applies the kept EQ again once it is not applied, for one
// monitor tick. Callers hold d.mu; report names the re-apply while it runs.
func (d *device) keepFrontEQ(ctx context.Context, report func(string)) {
	m := &d.eq
	if !m.keep || m.active || m.problem != "" || !d.powerOn() || controlBusy() || !d.idleKeys() || d.smartModeNow() {
		m.keepStable = 0
		return
	}
	n, e := d.readEQNative()
	if e != nil || n.Mode != 0 || n.Cache != 0 {
		m.keepStable = 0
		return
	}
	// Do not retry on the same native state after a failure.
	if m.keepBlocked != "" && m.keepBlocked == eqDigest(n.Block) {
		return
	}
	if m.keepStable++; m.keepStable < eqKeepStable {
		return
	}
	m.keepStable = 0
	gains := m.kept
	report("eq_apply")
	if e := d.applyFrontEQ(ctx, gains); e != nil {
		m.keepBlocked = eqDigest(n.Block)
		m.note("Front EQ could not be applied again: %v", e)
		return
	}
	m.keepBlocked = ""
	m.note("Front EQ applied again")
}

// note logs a state change of the kept EQ once.
func (m *eqController) note(format string, args ...any) {
	line := format
	if len(args) > 0 {
		line = fmt.Sprintf(format, args...)
	}
	if line != m.lastNote {
		m.lastNote = line
		log.Print(line)
	}
}

func validKeptGains(g [5]float64) bool {
	nonZero := false
	for _, v := range g {
		if !eqFinite(v) || v < -6 || v > 0 || v*2 != math.Trunc(v*2) {
			return false
		}
		if v != 0 {
			nonZero = true
		}
	}
	return nonZero
}

func loadKeptEQ(path string) ([5]float64, bool) {
	var out [5]float64
	b, e := os.ReadFile(path)
	if e != nil || len(b) > 256 {
		return out, false
	}
	var record struct {
		Gains []float64 `json:"gains_db"`
	}
	if json.Unmarshal(b, &record) != nil || len(record.Gains) != 5 {
		return out, false
	}
	copy(out[:], record.Gains)
	return out, validKeptGains(out)
}

func saveKeptEQ(path string, g [5]float64) error {
	if !validKeptGains(g) {
		return errors.New("invalid kept EQ")
	}
	raw, _ := json.Marshal(struct {
		Gains []float64 `json:"gains_db"`
	}{g[:]})
	tmp := path + ".new"
	_ = os.Remove(tmp)
	f, e := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	if _, e = f.Write(append(raw, '\n')); e == nil {
		e = f.Sync()
	}
	if c := f.Close(); e == nil {
		e = c
	}
	if e != nil {
		os.Remove(tmp)
		return e
	}
	return os.Rename(tmp, path)
}

func clearKeptEQ(path string) error {
	if e := os.Remove(path); e != nil && !os.IsNotExist(e) {
		return e
	}
	return nil
}

func (d *device) loadKeptFrontEQ() {
	m := &d.eq
	m.keptLoaded = true
	if g, ok := loadKeptEQ(eqKeepPath); ok {
		m.kept, m.keep = g, true
		log.Printf("Front EQ kept: %v dB", g)
	}
}

func (d *device) keepFrontEQGains(g [5]float64) {
	m := &d.eq
	m.kept, m.keep, m.keepBlocked = g, true, ""
	if e := saveKeptEQ(eqKeepPath, g); e != nil {
		log.Printf("Front EQ: cannot keep the EQ across restarts: %v", e)
	}
}

func (d *device) forgetKeptFrontEQ() {
	m := &d.eq
	m.kept, m.keep, m.keepBlocked, m.keepStable = [5]float64{}, false, "", 0
	if e := clearKeptEQ(eqKeepPath); e != nil {
		log.Printf("Front EQ: cannot remove the kept EQ: %v", e)
	}
}
