package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func startTimers(t *testing.T, level3 uint32) (*fakeDisplay, *displayManager, *timerManager, func()) {
	t.Helper()
	f, m, stop := startDisplay(t, level3)
	tm := newTimerManager(m, f, t.Logf)
	ctx, cancel := contextWithCancel()
	done := make(chan struct{})
	go func() { tm.Run(ctx); close(done) }()
	for {
		tm.mu.Lock()
		ok := tm.root != nil
		tm.mu.Unlock()
		if ok {
			break
		}
		time.Sleep(time.Millisecond)
	}
	return f, m, tm, func() { cancel(); <-done; stop() }
}

func startTimer(t *testing.T, tm *timerManager, params string) string {
	t.Helper()
	r, err := parseTimerStart(json.RawMessage(params))
	if err != nil {
		t.Fatal(err)
	}
	id, err := tm.Start(r)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func waitTimers(t *testing.T, f *fakeDisplay, tm *timerManager, n int, limit time.Duration) {
	t.Helper()
	start := f.now()
	for len(tm.List()) != n {
		if f.now().Sub(start) > limit {
			t.Fatalf("timers %+v, want %d; screens %v", tm.List(), n, f.screens())
		}
		time.Sleep(time.Millisecond)
	}
}

// press simulates a key of the bar's remote: the flag rises for 300 ms.
func press(f *fakeDisplay) {
	f.set(vfdKeyFlag, 1)
	time.Sleep(300 * time.Millisecond / displayScale)
	f.set(vfdKeyFlag, 0)
}

func TestTimerTexts(t *testing.T) {
	for left, want := range map[time.Duration]string{
		0: "0'00", 59*time.Second + 100*time.Millisecond: "1'00", 299 * time.Second: "4'59",
		time.Hour + 4*time.Minute + 59*time.Second: "1H04'59", -time.Second: "0'00",
	} {
		if got := timerClock(left); got != want {
			t.Errorf("timerClock(%v) = %q, want %q", left, got, want)
		}
	}
	for _, c := range []struct {
		name string
		left time.Duration
		want string
	}{{"PIZZA", 274 * time.Second, "PIZZA 4'34"}, {"", 274 * time.Second, "4'34"}, {strings.Repeat("A", 26), 754 * time.Second, "12'34"}} {
		if got := timerReminder(c.name, c.left); got != c.want {
			t.Errorf("timerReminder(%q, %v) = %q, want %q", c.name, c.left, got, c.want)
		}
	}
	for name, want := range map[string]string{"PIZZA": "TIME'S UP PIZZA", "": "TIME'S UP - PRESS ANY KEY", strings.Repeat("A", 25): strings.Repeat("A", 25)} {
		if got := timerAlarmText(name); got != want {
			t.Errorf("timerAlarmText(%q) = %q, want %q", name, got, want)
		}
		if _, err := displayText(timerAlarmText(name)); err != nil {
			t.Errorf("alarm text %q breaks the display rules: %v", timerAlarmText(name), err)
		}
	}
	for _, left := range []time.Duration{299 * time.Second, 11*time.Hour + 59*time.Minute} {
		if _, err := displayText(timerClock(left)); err != nil {
			t.Errorf("clock %q breaks the display rules: %v", timerClock(left), err)
		}
	}
}

func TestTimerParameters(t *testing.T) {
	r, err := parseTimerStart(json.RawMessage(`{"duration_s":300}`))
	if err != nil || r != (timerRequest{For: 5 * time.Minute, Sound: "off"}) {
		t.Fatalf("no name: %+v %v", r, err)
	}
	r, err = parseTimerStart(json.RawMessage(`{"text":"pâtes","duration_s":600,"countdown":true}`))
	if err != nil || r != (timerRequest{Text: "PATES", For: 10 * time.Minute, Countdown: true, Sound: "off"}) {
		t.Fatalf("folded text: %+v %v", r, err)
	}
	for _, in := range []string{`{}`, `{"duration_s":0}`, `{"duration_s":43201}`, `{"duration_s":60,"text":"50%"}`, `{"duration_s":60,"text":"STOP!"}`, `{"duration_s":60,"text":"A:B"}`, `{"duration_s":60,"color":"red"}`} {
		if _, err := parseTimerStart(json.RawMessage(in)); err == nil {
			t.Errorf("%s: want an error", in)
		}
	}
	if _, err := parseTimerCancel(nil); err == nil {
		t.Error("cancel without id: want an error")
	}
	if _, err := parseDisplayShow(json.RawMessage(`{"text":"a","id":"timer-t1"}`)); err == nil {
		t.Error("a message may not use a timer id")
	}
}

func TestTimerRingsUntilAKeyIsPressed(t *testing.T) {
	f, _, tm, stop := startTimers(t, vfdLevel3StandbyBlank)
	defer stop()
	startTimer(t, tm, `{"text":"pates","duration_s":3}`)
	if l := tm.List(); len(l) != 1 || l[0].Ringing || l[0].RemainingMS <= 0 {
		t.Fatalf("running: %+v", l)
	}
	if count(f.screens(), "text:TIME'S UP PATES") != 0 {
		t.Fatal("shown before the end without countdown")
	}
	waitScreen(t, f, "text:TIME'S UP PATES", 20*time.Second)
	if l := tm.List(); len(l) != 1 || !l[0].Ringing || l[0].RemainingMS != 0 {
		t.Fatalf("ringing: %+v", l)
	}
	time.Sleep(20 * time.Second / displayScale) // more than two passes
	alarm := f.screens()
	for i, s := range alarm {
		if s == "text: " && i > 0 && alarm[i-1] == "text:TIME'S UP PATES" {
			t.Fatalf("a blank between alarm passes: %v", alarm)
		}
	}
	press(f)
	waitTimers(t, f, tm, 0, 10*time.Second)
	waitScreen(t, f, "black", 10*time.Second)
	if f.read(vfdLevel3) != vfdLevel3StandbyBlank || f.read(vfdLevel2) != vfdMessageNone {
		t.Fatalf("display not handed back: %v", f.mem)
	}
}

func TestTimerKeyHeldBeforeTheAlarmDoesNotStopIt(t *testing.T) {
	f, _, tm, stop := startTimers(t, vfdLevel3None)
	defer stop()
	f.set(vfdKeyFlag, 1) // a key held (or sent by the remote) when the alarm starts
	startTimer(t, tm, `{"duration_s":1}`)
	waitScreen(t, f, "text:TIME'S UP - PRESS ANY KEY", 20*time.Second)
	time.Sleep(2 * time.Second / displayScale)
	if len(tm.List()) != 1 {
		t.Fatal("a key already down stopped the alarm")
	}
	f.set(vfdKeyFlag, 0)
	time.Sleep(time.Second / displayScale)
	press(f)
	waitTimers(t, f, tm, 0, 10*time.Second)
}

func TestTimerAlarmEndsAfterTenMinutes(t *testing.T) {
	f, _, tm, stop := startTimers(t, vfdLevel3None)
	defer stop()
	startTimer(t, tm, `{"duration_s":1}`)
	waitScreen(t, f, "text:TIME'S UP - PRESS ANY KEY", 20*time.Second)
	start := f.now()
	waitTimers(t, f, tm, 0, 15*time.Minute)
	// Counted from the end of the timer; the screen shows it a moment later.
	if took := f.now().Sub(start); took < timerAlarmFor-5*time.Second || took > timerAlarmFor+time.Minute {
		t.Fatalf("alarm lasted %v", took)
	}
}

// screensOf lists the texts shown, without blanks or repeats.
func screensOf(f *fakeDisplay) []string {
	var shown []string
	for _, s := range f.screens() {
		if strings.HasPrefix(s, "text:") && s != "text: " && (len(shown) == 0 || shown[len(shown)-1] != s) {
			shown = append(shown, strings.TrimPrefix(s, "text:"))
		}
	}
	return shown
}

func TestTimerAloneShowsItsClockSecondBySecond(t *testing.T) {
	f, m, tm, stop := startTimers(t, vfdLevel3None)
	defer stop()
	startTimer(t, tm, `{"text":"pizza","duration_s":6,"countdown":true}`)
	waitScreen(t, f, "text:TIME'S UP PIZZA", time.Minute)
	var got []string
	for _, s := range screensOf(f) {
		if s != "0'00" { // may show for an instant before the alarm
			got = append(got, s)
		}
	}
	want := []string{"0'06", "0'05", "0'04", "0'03", "0'02", "0'01", "TIME'S UP PIZZA"}
	// The clock starts within a second of the start: allow 0'06 to be missed.
	if j := strings.Join(got, "|"); j != strings.Join(want, "|") && j != strings.Join(want[1:], "|") {
		t.Fatalf("shown %v", got)
	}
	all := f.screens()
	for i, s := range all {
		if s == "text: " && i > 0 && strings.HasPrefix(all[i-1], "text:0'") {
			t.Fatalf("a blank in the countdown: %v", all)
		}
	}
	if err := tm.Cancel(tm.List()[0].ID); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, f, m, 10*time.Second)
}

func TestTimersSeveralShowMinuteRoundsInTurn(t *testing.T) {
	f, _, tm, stop := startTimers(t, vfdLevel3None)
	defer stop()
	startTimer(t, tm, `{"text":"pates","duration_s":605,"countdown":true}`)
	waitPrefix(t, f, "text:10'0", 10*time.Second) // alone: its clock
	startTimer(t, tm, `{"text":"lessive","duration_s":790,"countdown":true}`)
	// Rounds follow PATES, the first to end: at 10 min left, both in turn,
	// each with its time when its pass starts (LESSIVE about 7 s later).
	waitPrefix(t, f, "text:PATES 9'5", time.Minute) // 10 min left, when its pass starts
	waitPrefix(t, f, "text:LESSIVE 1", 30*time.Second)
	waitPrefix(t, f, "text:PATES 8'5", 2*time.Minute)
	waitPrefix(t, f, "text:LESSIVE 1", 30*time.Second)
	rounds := false
	for _, s := range screensOf(f) {
		if strings.HasPrefix(s, "LESSIVE ") {
			var m, sec int
			fmt.Sscanf(strings.TrimPrefix(s, "LESSIVE "), "%d'%d", &m, &sec)
			// 790 s - 5 s at the first round: a stale time would read 13'05.
			if left := m*60 + sec; left > 780 {
				t.Fatalf("LESSIVE time taken before its pass: %s", s)
			}
		}
		rounds = rounds || strings.HasPrefix(s, "PATES ")
		if rounds && !strings.Contains(s, " ") {
			t.Fatalf("the clock came back with two timers: %v", screensOf(f))
		}
	}
}

func TestTimerClockComesBackWhenAloneAgain(t *testing.T) {
	f, _, tm, stop := startTimers(t, vfdLevel3None)
	defer stop()
	startTimer(t, tm, `{"text":"pates","duration_s":300,"countdown":true}`)
	other := startTimer(t, tm, `{"text":"lessive","duration_s":600}`)
	time.Sleep(3 * time.Second / displayScale)
	if err := tm.Cancel(other); err != nil {
		t.Fatal(err)
	}
	// Alone again: the clock of PATES (about 4'57) comes back.
	waitPrefix(t, f, "text:4'5", 20*time.Second)
}

// waitPrefix waits until the screen shows a text starting with prefix.
func waitPrefix(t *testing.T, f *fakeDisplay, prefix string, limit time.Duration) {
	t.Helper()
	start := f.now()
	for {
		f.mu.Lock()
		v := f.visible
		f.mu.Unlock()
		if strings.HasPrefix(v, prefix) {
			return
		}
		if f.now().Sub(start) > limit {
			t.Fatalf("screen never showed %q…: %v", prefix, f.screens())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestTimerSameTextRefusedAndCancel(t *testing.T) {
	f, m, tm, stop := startTimers(t, vfdLevel3None)
	defer stop()
	id := startTimer(t, tm, `{"text":"Pâtes","duration_s":600,"countdown":true}`)
	time.Sleep(2 * time.Second / displayScale) // its clock is showing
	r, _ := parseTimerStart(json.RawMessage(`{"text":"PATES","duration_s":60}`))
	if _, err := tm.Start(r); err == nil || !strings.Contains(err.Error(), "PATES is already running") {
		t.Fatalf("same text: %v", err)
	}
	other := startTimer(t, tm, `{"text":"lessive","duration_s":60}`)
	if l := tm.List(); len(l) != 2 || l[0].ID != other {
		t.Fatalf("list sorted by time left: %+v", l)
	}
	// Cancelling every message leaves timers alone.
	m.Cancel("")
	if err := tm.Cancel(id); err != nil {
		t.Fatal(err)
	}
	if err := tm.Cancel(id); err != errTimerNotFound {
		t.Fatalf("second cancel: %v", err)
	}
	if err := tm.Cancel(other); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, f, m, 10*time.Second)
	if count(f.screens(), "text:LESSIVE") != 0 {
		t.Fatal("a cancelled timer rang")
	}
}

func TestTimersRingInTurn(t *testing.T) {
	f, _, tm, stop := startTimers(t, vfdLevel3None)
	defer stop()
	startTimer(t, tm, `{"text":"pates","duration_s":1}`)
	startTimer(t, tm, `{"text":"oeufs","duration_s":1}`)
	waitScreen(t, f, "text:TIME'S UP PATES", 20*time.Second)
	waitScreen(t, f, "text:TIME'S UP OEUFS", 30*time.Second)
	waitScreen(t, f, "text:TIME'S UP PATES", 30*time.Second)
	press(f) // stops the one on screen
	waitTimers(t, f, tm, 1, 10*time.Second)
}

func TestTimerRemindersNameTheTimerWhenSeveralRun(t *testing.T) {
	f, _, tm, stop := startTimers(t, vfdLevel3None)
	defer stop()
	startTimer(t, tm, `{"text":"pates","duration_s":125,"countdown":true}`)
	startTimer(t, tm, `{"text":"lessive","duration_s":600}`)
	waitPrefix(t, f, "text:PATES 1'5", 2*time.Minute)
	waitPrefix(t, f, "text:PATES 0'5", 2*time.Minute)
}

func TestTimerOnlyOneWithoutName(t *testing.T) {
	_, _, tm, stop := startTimers(t, vfdLevel3None)
	defer stop()
	startTimer(t, tm, `{"duration_s":600}`)
	r, _ := parseTimerStart(json.RawMessage(`{"duration_s":60}`))
	if _, err := tm.Start(r); err == nil || !strings.Contains(err.Error(), "without a name") {
		t.Fatalf("second unnamed timer: %v", err)
	}
	startTimer(t, tm, `{"text":"pizza","duration_s":60}`)
}

func TestTimerLimit(t *testing.T) {
	_, _, tm, stop := startTimers(t, vfdLevel3None)
	defer stop()
	for i := 0; i < timerMaxCount; i++ {
		startTimer(t, tm, `{"text":"T`+string(rune('A'+i))+`","duration_s":600}`)
	}
	r, _ := parseTimerStart(json.RawMessage(`{"text":"ONE MORE","duration_s":600}`))
	if _, err := tm.Start(r); err == nil {
		t.Fatal("more than the limit accepted")
	}
}

func contextWithCancel() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}
