package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestEQFollowsNativelyOnlyMeasuredControls(t *testing.T) {
	for _, action := range []string{"bass_next", "atmos_next", "rear_next", "night_toggle", "standard", "smart", "input_tv", "input_hdmi", "input_bluetooth"} {
		if !eqFollowsNatively(action) || eqResetBefore(action, true) {
			t.Fatalf("%s still removes the EQ first", action)
		}
	}
	// Not measured with an EQ applied: keep removing it first.
	for _, action := range []string{"power", "bluetooth_pair", "center_next", "calibrate", "sub_pair", "av_sync_set"} {
		if eqFollowsNatively(action) {
			t.Fatalf("%s follows natively without device evidence", action)
		}
	}
	if !eqResetBefore("power", true) {
		t.Fatal("power from on still removes the EQ first")
	}
}

func TestEQFollowDecision(t *testing.T) {
	native := eqNativeState{Mode: 5, outer: 0x100, SampleRate: 48000}
	expected := []byte{1, 2, 3}
	at := func(mode, cache uint32, block []byte) eqNativeState {
		return eqNativeState{Mode: mode, Cache: cache, outer: 0x100, SampleRate: 48000, Block: block}
	}
	cases := []struct {
		name    string
		n       eqNativeState
		err     error
		smart   bool
		changed bool
		want    eqFollow
	}{
		{"expected block", at(5, 0, expected), nil, false, false, eqFollowOK},
		{"recomputed after Bass/Atmos/Rear", at(5, 0, []byte{9}), nil, false, true, eqFollowLevels},
		{"other block, same levels", at(5, 0, []byte{9}), nil, false, false, eqFollowReset},
		{"dropped by a source change", at(0, 0, []byte{7}), nil, false, false, eqFollowDropped},
		{"dropped, levels changed too", at(0, 0, []byte{7}), nil, false, true, eqFollowDropped},
		{"Smart Mode", at(1, 1, []byte{7}), nil, true, false, eqFollowSmart},
		{"unqualified route", eqNativeState{}, errEQRoute, false, false, eqFollowReset},
		{"Smart, unreadable", eqNativeState{}, errEQRoute, true, false, eqFollowSmart},
		{"other audio instance", eqNativeState{Mode: 5, outer: 0x200, SampleRate: 48000, Block: []byte{9}}, nil, false, true, eqFollowReset},
		{"other sample rate", eqNativeState{Mode: 5, outer: 0x100, SampleRate: 44100, Block: []byte{9}}, nil, false, true, eqFollowReset},
		{"unknown mode", at(1, 0, []byte{9}), nil, false, true, eqFollowReset},
	}
	for _, c := range cases {
		if got := eqFollowDecision(c.n, c.err, native, expected, c.smart, c.changed); got != c.want {
			t.Fatalf("%s: got %d want %d", c.name, got, c.want)
		}
	}
	if eqFollowDecision(eqNativeState{}, errors.New("soundbar restarted"), native, expected, false, false) != eqFollowReset {
		t.Fatal("other read errors keep the reset path")
	}
}

func TestKeptEQFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "front-eq.json")
	if _, ok := loadKeptEQ(path); ok {
		t.Fatal("missing file kept an EQ")
	}
	want := [5]float64{0, 0, 0, -6, -6}
	if e := saveKeptEQ(path, want); e != nil {
		t.Fatal(e)
	}
	if got, ok := loadKeptEQ(path); !ok || got != want {
		t.Fatalf("kept %v %v", got, ok)
	}
	if info, e := os.Stat(path); e != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("mode %v %v", info, e)
	}
	for _, bad := range [][5]float64{{}, {1, 0, 0, 0, 0}, {-6.5, 0, 0, 0, 0}, {-0.25, 0, 0, 0, 0}} {
		if saveKeptEQ(path, bad) == nil {
			t.Fatalf("invalid EQ kept: %v", bad)
		}
	}
	for _, raw := range []string{`garbage`, `{"gains_db":[0,0,0]}`, `{"gains_db":[0,0,0,0,0]}`, `{"gains_db":[0,0,0,0,3]}`} {
		os.WriteFile(path, []byte(raw), 0600)
		if _, ok := loadKeptEQ(path); ok {
			t.Fatalf("accepted %s", raw)
		}
	}
	if e := clearKeptEQ(path); e != nil {
		t.Fatal(e)
	}
	if e := clearKeptEQ(path); e != nil {
		t.Fatal("clearing twice", e)
	}
	if _, e := os.Stat(path + ".new"); !os.IsNotExist(e) {
		t.Fatal("temporary file left")
	}
}

func TestKeptEQForgetAndKeep(t *testing.T) {
	saved := eqKeepPath
	eqKeepPath = filepath.Join(t.TempDir(), "front-eq.json")
	defer func() { eqKeepPath = saved }()
	d := &device{}
	d.keepFrontEQGains([5]float64{-3, 0, 0, 0, 0})
	if !d.eq.keep {
		t.Fatal("not kept")
	}
	d2 := &device{}
	d2.loadKeptFrontEQ()
	if !d2.eq.keep || d2.eq.kept != [5]float64{-3, 0, 0, 0, 0} {
		t.Fatal("kept EQ not restored after a restart")
	}
	d2.forgetKeptFrontEQ()
	d3 := &device{}
	d3.loadKeptFrontEQ()
	if d3.eq.keep {
		t.Fatal("Reset did not forget the kept EQ")
	}
}

func TestKeptEQStatusShowsTheWaitingEQ(t *testing.T) {
	d := &device{}
	d.eq.keep, d.eq.kept = true, [5]float64{0, 0, 0, -6, -6}
	// Standby: the native reader is not consulted.
	out := d.frontEQStatus(state{Available: true, Power: "Standby"})
	if !out.Kept || out.Status != "waiting" || out.Error != eqKeepWaiting || out.Gains[4] != -6 {
		t.Fatalf("%+v", out)
	}
	d.eq.keep = false
	if out = d.frontEQStatus(state{Available: true, Power: "Standby"}); out.Kept || out.Status == "waiting" {
		t.Fatalf("%+v", out)
	}
}

// A failed reset used to be retried, and reported as a control, every
// monitor tick: the page flashed "Another control is running" every two
// seconds. It is retried only once the native state changes.
func TestEQFollowRetryOnlyOnANewNativeState(t *testing.T) {
	a := eqNativeState{Block: []byte{1, 2, 3}, Mode: 0, Cache: 0, Element: 0x840000, SampleRate: 48000}
	b := a
	b.Block = []byte{1, 2, 4}
	c := a
	c.Mode = 5
	keyA, keyB, keyC := eqFollowKey(a, nil), eqFollowKey(b, nil), eqFollowKey(c, nil)
	keyErr := eqFollowKey(eqNativeState{}, errors.New("native EQ is not ready on this audio route"))
	if keyA == keyB || keyA == keyC || keyA == keyErr || keyA != eqFollowKey(a, nil) {
		t.Fatalf("keys must follow the native state: %q %q %q %q", keyA, keyB, keyC, keyErr)
	}
	for _, d := range []eqFollow{eqFollowDropped, eqFollowReset} {
		if !eqFollowRetry(d, "", keyA) {
			t.Errorf("decision %d: first attempt skipped", d)
		}
		if eqFollowRetry(d, keyA, keyA) {
			t.Errorf("decision %d: retried on the state where it failed", d)
		}
		if !eqFollowRetry(d, keyA, keyB) || !eqFollowRetry(d, keyErr, keyA) {
			t.Errorf("decision %d: not retried after the native state changed", d)
		}
	}
	for _, d := range []eqFollow{eqFollowOK, eqFollowLevels, eqFollowSmart} {
		if !eqFollowRetry(d, keyA, keyA) {
			t.Errorf("decision %d: blocked although it does not reset", d)
		}
	}
}

// An EQ chosen while it cannot be applied (standby, Smart Mode, another
// audio route) is kept and applied later; anything else is applied or
// refused now, as before.
func TestEQDeferFor(t *testing.T) {
	other := errors.New("audio process changed")
	for _, c := range []struct {
		name        string
		problem     string
		available   bool
		power, mode string
		active      bool
		routeErr    error
		want        string
	}{
		{"standby", "", true, "Standby", "Smart", false, nil, "standby"},
		{"smart", "", true, "On", "Smart", false, nil, "Smart Mode"},
		{"unqualified route", "", true, "On", "Standard", false, errEQRoute, "this audio route"},
		{"wrapped route error", "", true, "On", "Standard", false, fmt.Errorf("read: %w", errEQRoute), "this audio route"},
		{"applicable now", "", true, "On", "Standard", false, nil, ""},
		{"other read error", "", true, "On", "Standard", false, other, ""},
		{"pending problem", "EQ reset pending: x", true, "Standby", "Smart", false, nil, ""},
		{"applied EQ", "", true, "On", "Smart", true, nil, ""},
		{"state unavailable", "", false, "", "", false, nil, ""},
	} {
		if got := eqDeferFor(c.problem, c.available, c.power, c.mode, c.active, c.routeErr); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// A reset is confirmed by the native preset (mode 0, cache 0); a differing
// block is only logged, and the log says when only the bass offset differs.
func TestEQBlockDiff(t *testing.T) {
	a := make([]byte, eqBlockSize)
	b := append([]byte(nil), a...)
	if n, only := eqBlockDiff(a, b); n != 0 || only {
		t.Fatalf("identical: %d %v", n, only)
	}
	b[0xdac], b[0xdaf] = 1, 2
	if n, only := eqBlockDiff(a, b); n != 2 || !only {
		t.Fatalf("bass offset only: %d %v", n, only)
	}
	b[0x10] = 3
	if n, only := eqBlockDiff(a, b); n != 3 || only {
		t.Fatalf("other bytes: %d %v", n, only)
	}
	if n, only := eqBlockDiff(a, b[:10]); n != eqBlockSize || only {
		t.Fatalf("length mismatch: %d %v", n, only)
	}
}
