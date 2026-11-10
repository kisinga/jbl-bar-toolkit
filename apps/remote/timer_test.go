package main

import (
	"context"
	"encoding/json"
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

func TestTimerClock(t *testing.T) {
	for left, want := range map[time.Duration]string{
		0: "0'00", 59*time.Second + 100*time.Millisecond: "1'00", 5 * time.Minute: "5'00", 299 * time.Second: "4'59",
		time.Hour + 4*time.Minute + 59*time.Second: "1H04'59", -time.Second: "0'00",
	} {
		if got := timerClock(left); got != want {
			t.Errorf("timerClock(%v) = %q, want %q", left, got, want)
		}
	}
	if got := countdownText("PATES", 299*time.Second); got != "PATES 4'59" {
		t.Errorf("with the name: %q", got)
	}
	if got := countdownText("LA LESSIVE EST FINIE", 299*time.Second); got != "4'59" {
		t.Errorf("name too long for 20 characters: %q", got)
	}
}

func TestTimerParameters(t *testing.T) {
	r, err := parseTimerStart(json.RawMessage(`{"duration_s":300}`))
	if err != nil || r != (timerRequest{Text: "TIMER!", For: 5 * time.Minute}) {
		t.Fatalf("default text: %+v %v", r, err)
	}
	r, err = parseTimerStart(json.RawMessage(`{"text":"pâtes","duration_s":600,"countdown":true}`))
	if err != nil || r != (timerRequest{Text: "PATES", For: 10 * time.Minute, Countdown: true}) {
		t.Fatalf("folded text: %+v %v", r, err)
	}
	for _, in := range []string{`{}`, `{"duration_s":0}`, `{"duration_s":43201}`, `{"duration_s":60,"text":"50%"}`, `{"duration_s":60,"color":"red"}`} {
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
	if count(f.screens(), "text:PATES") != 0 {
		t.Fatal("shown before the end without countdown")
	}
	waitScreen(t, f, "text:PATES", 20*time.Second)
	if l := tm.List(); len(l) != 1 || !l[0].Ringing || l[0].RemainingMS != 0 {
		t.Fatalf("ringing: %+v", l)
	}
	time.Sleep(2 * time.Second / displayScale)
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
	waitScreen(t, f, "text:TIMER!", 20*time.Second)
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
	waitScreen(t, f, "text:TIMER!", 20*time.Second)
	start := f.now()
	waitTimers(t, f, tm, 0, 15*time.Minute)
	// Counted from the end of the timer; the screen shows it a moment later.
	if took := f.now().Sub(start); took < timerAlarmFor-5*time.Second || took > timerAlarmFor+time.Minute {
		t.Fatalf("alarm lasted %v", took)
	}
}

func TestTimerCountdownUpdatesInPlace(t *testing.T) {
	f, m, tm, stop := startTimers(t, vfdLevel3None)
	defer stop()
	startTimer(t, tm, `{"text":"pates","duration_s":5,"countdown":true}`)
	waitScreen(t, f, "text:PATES 0'03", 20*time.Second)
	waitScreen(t, f, "text:PATES 0'01", 20*time.Second)
	waitScreen(t, f, "text:PATES", 20*time.Second) // the alarm replaces the countdown
	got := f.screens()
	for i, s := range got {
		if s == "text: " && i > 0 && strings.HasPrefix(got[i-1], "text:PATES 0'") {
			t.Fatalf("a blank between countdown steps: %v", got)
		}
	}
	m.mu.Lock()
	jobs := len(m.jobs)
	m.mu.Unlock()
	if jobs != 1 {
		t.Fatalf("display jobs %d, want the alarm only", jobs)
	}
	if err := tm.Cancel(tm.List()[0].ID); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, f, m, 10*time.Second)
}

func TestTimerSameTextRefusedAndCancel(t *testing.T) {
	f, m, tm, stop := startTimers(t, vfdLevel3None)
	defer stop()
	id := startTimer(t, tm, `{"text":"Pâtes","duration_s":600,"countdown":true}`)
	r, _ := parseTimerStart(json.RawMessage(`{"text":"PATES","duration_s":60}`))
	if _, err := tm.Start(r); err == nil || !strings.Contains(err.Error(), "PATES is already running") {
		t.Fatalf("same text: %v", err)
	}
	other := startTimer(t, tm, `{"text":"lessive","duration_s":60}`)
	if l := tm.List(); len(l) != 2 || l[0].ID != other {
		t.Fatalf("list sorted by time left: %+v", l)
	}
	// Cancelling every message leaves the countdown alone.
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
	waitScreen(t, f, "text:PATES", 20*time.Second)
	waitScreen(t, f, "text:OEUFS", 30*time.Second)
	waitScreen(t, f, "text:PATES", 30*time.Second)
	press(f) // stops the one on screen
	waitTimers(t, f, tm, 1, 10*time.Second)
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
