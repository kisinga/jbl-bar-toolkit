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

func clockOn(when string) clockSet {
	on := true
	return clockSet{Enabled: &on, When: &when}
}

func clockOff() clockSet {
	off := false
	return clockSet{Enabled: &off}
}

// startClock runs a display manager and a clock on a fake display; the clock
// starts on (UTC, in standby only) unless off.
func startClock(t *testing.T, level3 uint32, on bool) (*fakeDisplay, *displayManager, *displayClock, func()) {
	t.Helper()
	saved := clockPollDivisor
	clockPollDivisor = displayScale
	f, m, stopDisplay := startDisplay(t, level3)
	c := newDisplayClock(m, f, filepath.Join(t.TempDir(), "clock.json"), t.Logf)
	if on {
		if err := c.Set(clockOn(clockStandby)); err != nil {
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
	if err := c.Set(clockOff()); err != nil {
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
	for _, bad := range []string{`{"enabled":true,"tz":"Mars/Olympus"}`, `{"tz":"UTC"}`, `{"when":"sometimes"}`} {
		if _, err := parseClockSet(json.RawMessage(bad)); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
	change, err := parseClockSet(json.RawMessage(`{"enabled":true,"tz":"Asia/Kolkata"}`))
	if err != nil || change.Enabled == nil || !*change.Enabled || change.TZ == nil {
		t.Fatal(change, err)
	}
	if err = c.Set(change); err != nil {
		t.Fatal(err)
	}
	if c.State()["when"] != clockStandby {
		t.Fatal("standby only by default:", c.State())
	}
	// India is UTC+5:30, without daylight saving time.
	_, _, text := c.now()
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
	// Turning it off keeps the time zone; "when" alone keeps it off.
	if err = again.Set(clockOff()); err != nil || again.State()["tz"] != "Asia/Kolkata" {
		t.Fatal(again.State(), err)
	}
	always := clockAlways
	if err = again.Set(clockSet{When: &always}); err != nil || again.State()["enabled"] != false || again.State()["when"] != clockAlways {
		t.Fatal(again.State(), err)
	}
}

func TestClockWithTheBarOnWhenAlways(t *testing.T) {
	f, m, c, stop := startClock(t, vfdLevel3None, false)
	defer stop()
	if err := c.Set(clockOn(clockAlways)); err != nil {
		t.Fatal(err)
	}
	waitClock(t, f, true, 10*time.Second)
	if f.read(vfdLevel3) != vfdLevel3None {
		t.Fatal("level 3 touched with the bar on")
	}
	// The volume (a level-2 message of the bar) comes first, then the clock again.
	f.set(vfdLevel2, 0)
	time.Sleep(2 * time.Second / displayScale)
	if clockShown(f) || f.read(vfdLevel2) != 0 {
		t.Fatal("the clock covered the volume")
	}
	f.set(vfdLevel2, vfdMessageNone)
	waitClock(t, f, true, 10*time.Second)
	// A message of ours comes first, its passes not interleaved, then the clock.
	show(t, m, `{"text":"colis","repeat":2}`)
	waitIdle(t, f, m, time.Minute)
	waitClock(t, f, true, 10*time.Second)
	// Standby only: the bar's idle screen comes back at once.
	standby := clockStandby
	if err := c.Set(clockSet{When: &standby}); err != nil {
		t.Fatal(err)
	}
	waitScreen(t, f, "background", 5*time.Second)
	// Always again, then the bar goes to standby (STDBY, then the blank screen):
	// the clock steps aside for STDBY and comes back over the blank screen.
	always := clockAlways
	if err := c.Set(clockSet{When: &always}); err != nil {
		t.Fatal(err)
	}
	waitClock(t, f, true, 10*time.Second)
	f.set(vfdLevel3, 23) // STDBY
	time.Sleep(2 * time.Second / displayScale)
	if f.read(vfdLevel2) != vfdMessageNone {
		t.Fatal("the clock kept its selector over STDBY")
	}
	f.set(vfdLevel3, vfdLevel3StandbyBlank)
	waitClock(t, f, true, 10*time.Second)
	if f.read(vfdLevel3) != vfdLevel3None {
		t.Fatal("standby screen not lifted")
	}
}
