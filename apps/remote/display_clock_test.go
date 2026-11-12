package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var clockScreen = regexp.MustCompile(`^text:\d\d \d\d$`)

// startClock runs a display manager and a clock on a fake display; the clock
// starts on (UTC) unless off.
func startClock(t *testing.T, level3 uint32, on bool) (*fakeDisplay, *displayManager, *displayClock, func()) {
	t.Helper()
	saved := clockPollDivisor
	clockPollDivisor = displayScale
	f, m, stopDisplay := startDisplay(t, level3)
	c := newDisplayClock(m, f, filepath.Join(t.TempDir(), "clock.json"), t.Logf)
	if on {
		if err := c.Set(true, nil); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	return f, m, c, func() {
		cancel()
		<-done
		stopDisplay()
		clockPollDivisor = saved
	}
}

func clockShown(f *fakeDisplay) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return clockScreen.MatchString(f.visible)
}

func waitClock(t *testing.T, f *fakeDisplay, want bool, limit time.Duration) {
	t.Helper()
	start := f.now()
	for clockShown(f) != want {
		if f.now().Sub(start) > limit {
			t.Fatalf("clock shown = %v after %v, want %v: %v", clockShown(f), limit, want, f.screens())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestClockShowsInStandbyAndGivesTheScreenBack(t *testing.T) {
	f, _, c, stop := startClock(t, vfdLevel3StandbyBlank, true)
	defer stop()
	waitClock(t, f, true, 10*time.Second)
	if f.read(vfdLevel3) != vfdLevel3None {
		t.Fatal("standby screen not lifted")
	}
	if err := c.Set(false, nil); err != nil {
		t.Fatal(err)
	}
	waitScreen(t, f, "black", 5*time.Second)
	if f.read(vfdLevel3) != vfdLevel3StandbyBlank || f.read(vfdLevel2) != vfdMessageNone {
		t.Fatalf("standby screen not given back: %v", f.mem)
	}
}

func TestClockNeverCoversTheBarWhenOn(t *testing.T) {
	f, _, _, stop := startClock(t, vfdLevel3None, true)
	defer stop()
	time.Sleep(10 * time.Second / displayScale)
	if got := f.screens(); strings.Join(got, "|") != "background" {
		t.Fatalf("screens %v: the clock covered the bar's own screen", got)
	}
}

func TestClockStepsAsideForMessagesAndComesBack(t *testing.T) {
	f, m, _, stop := startClock(t, vfdLevel3StandbyBlank, true)
	defer stop()
	waitClock(t, f, true, 10*time.Second)
	show(t, m, `{"text":"frigo","repeat":2}`)
	waitIdle(t, f, m, time.Minute)
	waitClock(t, f, true, 10*time.Second)
	// While the message ran, its passes were never interleaved with the clock.
	var seq []string
	for _, s := range f.screens() {
		if clockScreen.MatchString(s) {
			s = "clock"
		}
		if len(seq) == 0 || seq[len(seq)-1] != s {
			seq = append(seq, s)
		}
	}
	got := strings.Join(seq, "|")
	if !strings.Contains(got, "text:FRIGO|text: |text:FRIGO") || strings.Contains(got, "FRIGO|clock|text: ") {
		t.Fatalf("screens %v", got)
	}
}

func TestClockGivesWayToTheBar(t *testing.T) {
	f, _, _, stop := startClock(t, vfdLevel3StandbyBlank, true)
	defer stop()
	waitClock(t, f, true, 10*time.Second)
	f.set(vfdLevel2, 0) // VOL in standby
	waitScreen(t, f, "black", 5*time.Second)
	if f.read(vfdLevel3) != vfdLevel3StandbyBlank {
		t.Fatal("standby screen not put back under the bar's message")
	}
	f.set(vfdLevel2, vfdMessageNone)
	waitClock(t, f, true, 10*time.Second)
	f.set(vfdLevel3, 22) // HELLO: the bar wakes up
	time.Sleep(2 * time.Second / displayScale)
	if f.read(vfdLevel2) != vfdMessageNone {
		t.Fatal("the clock kept its selector over the bar's wake-up")
	}
	f.set(vfdLevel3, vfdLevel3None) // on: the bar's own screen stays
	time.Sleep(5 * time.Second / displayScale)
	if clockShown(f) {
		t.Fatal("the clock showed with the bar on")
	}
}

func TestClockSettingsAndTimeZone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clock.json")
	f := newFakeDisplay(vfdLevel3StandbyBlank)
	c := newDisplayClock(newDisplayManager(f, t.Logf), f, path, t.Logf)
	if _, _, err := parseClockSet(json.RawMessage(`{"enabled":true,"tz":"Mars/Olympus"}`)); err == nil {
		t.Fatal("unknown time zone accepted")
	}
	if _, _, err := parseClockSet(json.RawMessage(`{"tz":"UTC"}`)); err == nil {
		t.Fatal("missing enabled accepted")
	}
	enabled, tz, err := parseClockSet(json.RawMessage(`{"enabled":true,"tz":"Asia/Kolkata"}`))
	if err != nil || !enabled || tz == nil {
		t.Fatal(enabled, tz, err)
	}
	if err = c.Set(enabled, tz); err != nil {
		t.Fatal(err)
	}
	// India is UTC+5:30, without daylight saving time.
	_, text := c.now()
	if want := f.now().UTC().Add(330 * time.Minute).Format(clockFormat); text != want {
		t.Fatalf("time %s, want %s", text, want)
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), `"Asia/Kolkata"`) {
		t.Fatalf("not saved: %s %v", b, err)
	}
	again := newDisplayClock(newDisplayManager(f, t.Logf), f, path, t.Logf)
	if s := again.State(); s["enabled"] != true || s["tz"] != "Asia/Kolkata" {
		t.Fatalf("not restored: %v", s)
	}
	// Turning it off keeps the time zone.
	if err = again.Set(false, nil); err != nil || again.State()["tz"] != "Asia/Kolkata" {
		t.Fatal(again.State(), err)
	}
}
