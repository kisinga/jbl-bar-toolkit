package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeRestoreBar models the bar after the chime: a key may be refused while
// the wake automation runs, and power toggles.
type fakeRestoreBar struct {
	power, input string
	refuse       int // the next keys refused
	keys         []string
	volumeDone   int
}

func (b *fakeRestoreBar) ops() barRestoreOps {
	return barRestoreOps{
		hold:     func(context.Context) (func(), func(string), bool) { return func() {}, func(string) {}, true },
		volume:   func(context.Context) { b.volumeDone++ },
		snapshot: func() state { return state{Power: b.power, Input: b.input} },
		action: func(_ context.Context, key string) error {
			if b.refuse > 0 {
				b.refuse--
				return errors.New("Smart Mode automation is busy; retry shortly")
			}
			b.keys = append(b.keys, key)
			if key == "power" {
				if b.power == "On" {
					b.power = "Standby"
				} else {
					b.power = "On"
				}
			} else {
				b.input = map[string]string{"input_tv": "TV / ARC", "input_hdmi": "HDMI", "input_bluetooth": "Bluetooth"}[key]
			}
			return nil
		},
		sleep: func(context.Context, time.Duration) {},
	}
}

// Owner's bar, 10 Nov 2026: woken by the chime, it stayed on Chromecast after
// the wake automation selected Standard. A refused key is tried again.
func TestTimerRestoreTriesAgainAfterARefusedKey(t *testing.T) {
	bar := &fakeRestoreBar{power: "On", input: "Chromecast", refuse: 2}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !bar.ops().restore(ctx, "power", "HDMI") {
		t.Fatal("not put back")
	}
	if bar.power != "Standby" || len(bar.keys) != 1 || bar.volumeDone != 1 {
		t.Fatalf("power %s, keys %v, volume restored %d times", bar.power, bar.keys, bar.volumeDone)
	}
}

func TestTimerRestoreSource(t *testing.T) {
	bar := &fakeRestoreBar{power: "On", input: "Chromecast"}
	if !bar.ops().restore(context.Background(), "input_hdmi", "HDMI") || bar.input != "HDMI" || len(bar.keys) != 1 {
		t.Fatalf("input %s, keys %v", bar.input, bar.keys)
	}
	// Already back (the owner switched it): no key.
	same := &fakeRestoreBar{power: "On", input: "HDMI"}
	if !same.ops().restore(context.Background(), "input_hdmi", "HDMI") || len(same.keys) != 0 {
		t.Fatalf("keys %v", same.keys)
	}
	// The owner picked TV during the chime: the bar stays on, on TV.
	tv := &fakeRestoreBar{power: "On", input: "TV / ARC"}
	if !tv.ops().restore(context.Background(), "power", "HDMI") || len(tv.keys) != 0 || tv.power != "On" {
		t.Fatalf("keys %v, power %s", tv.keys, tv.power)
	}
	// Turned off meanwhile: power is not pressed (it would turn it on).
	off := &fakeRestoreBar{power: "Standby", input: "Chromecast"}
	if !off.ops().restore(context.Background(), "power", "HDMI") || len(off.keys) != 0 {
		t.Fatalf("keys %v", off.keys)
	}
}

func TestTimerRestoreGivesUpWhenTheTimeIsOver(t *testing.T) {
	bar := &fakeRestoreBar{power: "On", input: "Chromecast", refuse: 1 << 30}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ops := bar.ops()
	ops.sleep = func(ctx context.Context, d time.Duration) { sleepCtx(ctx, 5*time.Millisecond) }
	if ops.restore(ctx, "power", "HDMI") {
		t.Fatal("reported put back")
	}
}

// A wake by the chime: the automation applies nothing and remembers nothing.
func TestTimerWakeLeavesTheModeAlone(t *testing.T) {
	var w wakeTracker
	s := wakeSample{identity: "native:1", power: 1, system: 0, mode: 3, source: 2, idle: true}
	w.observeFor(s, false)
	w.handled, w.timer = true, true // as the loop does while the timer sound runs
	for i := 0; i < 70; i++ {
		if status, apply := w.observeFor(s, false); status != "TIMER_WAKE" || apply {
			t.Fatalf("%s %v", status, apply)
		}
		if _, kept := w.settle(s); kept {
			t.Fatal("a mode remembered after a timer wake")
		}
	}
	// The next wake is a normal one again.
	w.observeFor(wakeSample{identity: "native:1", power: 0}, false)
	w.observeFor(s, false)
	w.observeFor(s, false)
	if _, apply := w.observeFor(s, false); !apply {
		t.Fatal("the next wake does not apply Standard")
	}
}
