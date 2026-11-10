package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDisplay is bdpprog's display state with a fast clock: displayScale
// simulated milliseconds pass per real millisecond.
const displayScale = 50

type fakeDisplay struct {
	mu      sync.Mutex
	origin  time.Time
	mem     map[int64]uint32
	text    string
	gateOff bool     // simulate another control holding the gate
	own     bool     // simulate keys the remote sends itself
	shown   []string // text visible on screen at each change
	visible string   // what the screen shows now
	trace   []string // level-2 / level-3 changes, for failures
}

func newFakeDisplay(level3 uint32) *fakeDisplay {
	f := &fakeDisplay{origin: time.Now(), mem: map[int64]uint32{
		vfdLevel2: vfdMessageNone, vfdLevel3: level3, vfdOverlay: vfdOverlayNone, vfdTickPeriod: 10, vfdTempTick: 0,
	}}
	f.render()
	return f
}

func (f *fakeDisplay) now() time.Time {
	return f.origin.Add(time.Since(f.origin) * displayScale)
}

func (f *fakeDisplay) ownKeys() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.own
}

func (f *fakeDisplay) sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d / displayScale)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// render mirrors the priority function 0x624190: level 3 first, then level 2.
func (f *fakeDisplay) render() {
	v := "background"
	switch l3 := f.mem[vfdLevel3]; {
	case l3 == vfdLevel3StandbyBlank:
		v = "black"
	case l3 != vfdLevel3None:
		v = "native-l3"
	case f.mem[vfdLevel2] == vfdMessageCustom:
		v = "text:" + f.text
	case f.mem[vfdLevel2] != vfdMessageNone:
		v = "native-l2"
	}
	if v != f.visible {
		f.visible = v
		f.shown = append(f.shown, v)
	}
}

func (f *fakeDisplay) set(address int64, v uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mem[address] = v
	f.render()
}

func (f *fakeDisplay) step(force bool, fn func(displayMem) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gateOff && !force {
		return errDisplayGate
	}
	err := fn(fakeDisplayMem{f})
	f.render()
	return err
}

func (f *fakeDisplay) screens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.shown...)
}

func (f *fakeDisplay) read(address int64) uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mem[address]
}

type fakeDisplayMem struct{ f *fakeDisplay }

func (m fakeDisplayMem) read32(a int64) (uint32, error) { return m.f.mem[a], nil }
func (m fakeDisplayMem) write32(a int64, v uint32) error {
	m.f.mem[a] = v
	m.f.trace = append(m.f.trace, "")
	return nil
}
func (m fakeDisplayMem) writeText(t string) error { m.f.text = t; m.f.render(); return nil }
func (m fakeDisplayMem) keyDown() (bool, error)   { return m.f.mem[vfdKeyFlag] != 0, nil }
func (m fakeDisplayMem) tick() (uint32, error) {
	return uint32(m.f.now().Sub(m.f.origin).Milliseconds() / 10), nil
}

func startDisplay(t *testing.T, level3 uint32) (*fakeDisplay, *displayManager, func()) {
	t.Helper()
	f := newFakeDisplay(level3)
	m := newDisplayManager(f, t.Logf)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	for {
		m.mu.Lock()
		ok := m.root != nil
		m.mu.Unlock()
		if ok {
			break
		}
		time.Sleep(time.Millisecond)
	}
	return f, m, func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("display manager did not stop")
		}
	}
}

// waitIdle waits until no message is showing or queued (simulated limit).
func waitIdle(t *testing.T, f *fakeDisplay, m *displayManager, limit time.Duration) time.Duration {
	t.Helper()
	start := f.now()
	for {
		m.mu.Lock()
		idle := len(m.jobs) == 0 && m.owner == nil
		m.mu.Unlock()
		if idle {
			return f.now().Sub(start)
		}
		if f.now().Sub(start) > limit {
			t.Fatalf("messages still running after %v: %v", limit, f.screens())
		}
		time.Sleep(time.Millisecond)
	}
}

// waitScreen waits until the screen shows want (simulated limit).
func waitScreen(t *testing.T, f *fakeDisplay, want string, limit time.Duration) {
	t.Helper()
	start := f.now()
	for {
		f.mu.Lock()
		v := f.visible
		f.mu.Unlock()
		if v == want {
			return
		}
		if f.now().Sub(start) > limit {
			t.Fatalf("screen never showed %q: %v", want, f.screens())
		}
		time.Sleep(time.Millisecond)
	}
}

func count(screens []string, want string) int {
	n := 0
	for _, s := range screens {
		if s == want {
			n++
		}
	}
	return n
}

func show(t *testing.T, m *displayManager, params string) {
	t.Helper()
	r, err := parseDisplayShow(json.RawMessage(params))
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Show(r); err != nil {
		t.Fatal(err)
	}
}

func TestDisplayTextFolding(t *testing.T) {
	for raw, want := range map[string]string{
		"frigo fermé":         "FRIGO FERME",
		"  Porte   ouverte. ": "PORTE OUVERTE.",
		"Œuf – côté":          "OEUF - COTE",
		"l’entrée":            "L'ENTREE",
	} {
		got, err := displayText(raw)
		if err != nil || got != want {
			t.Errorf("displayText(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "   ", strings.Repeat("A", 32), "50% OFF", "STOP!", "A:B", "€"} {
		if got, err := displayText(raw); err == nil {
			t.Errorf("displayText(%q) = %q, want an error", raw, got)
		}
	}
}

func TestDisplayParameters(t *testing.T) {
	good := map[string]displayRequest{
		`{"text":"frigo"}`:                             {ID: "default", Text: "FRIGO", Mode: displayPasses, Passes: 1},
		`{"text":"frigo","id":"fridge","repeat":4}`:    {ID: "fridge", Text: "FRIGO", Mode: displayPasses, Passes: 4},
		`{"text":"frigo","duration_s":300}`:            {ID: "default", Text: "FRIGO", Mode: displayUntil, Passes: 1, For: 5 * time.Minute},
		`{"text":"frigo","forever":true,"id":"a_b-1"}`: {ID: "a_b-1", Text: "FRIGO", Mode: displayForever, Passes: 1},
	}
	for in, want := range good {
		got, err := parseDisplayShow(json.RawMessage(in))
		if err != nil || got != want {
			t.Errorf("%s: %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{`{}`, `{"text":"a","repeat":0}`, `{"text":"a","repeat":100}`, `{"text":"a","duration_s":0}`,
		`{"text":"a","duration_s":86401}`, `{"text":"a","repeat":2,"forever":true}`, `{"text":"a","id":"Fridge"}`,
		`{"text":"a","colour":"red"}`, `{"text":"a","id":"` + strings.Repeat("x", 33) + `"}`} {
		if _, err := parseDisplayShow(json.RawMessage(in)); err == nil {
			t.Errorf("%s: want an error", in)
		}
	}
	if id, err := parseDisplayCancel(json.RawMessage(`{"id":"fridge"}`)); err != nil || id != "fridge" {
		t.Errorf("cancel fridge: %q %v", id, err)
	}
	if id, err := parseDisplayCancel(nil); err != nil || id != "" {
		t.Errorf("cancel all: %q %v", id, err)
	}
	if _, err := parseDisplayCancel(json.RawMessage(`{"id":"BAD ID"}`)); err == nil {
		t.Error("cancel with a bad id: want an error")
	}
}

func TestDisplayPassTime(t *testing.T) {
	if got := displayPassTime("HELLO"); got != 4950*time.Millisecond {
		t.Errorf("short text: %v", got)
	}
	if got := displayPassTime("HELLO FABIEN"); got != 6600*time.Millisecond {
		t.Errorf("12 characters: %v (measured on the bar: 6.75 s)", got)
	}
}

func TestDisplayPassesWithBlanksBarOn(t *testing.T) {
	f, m, stop := startDisplay(t, vfdLevel3None)
	defer stop()
	show(t, m, `{"text":"frigo","repeat":2}`)
	took := waitIdle(t, f, m, time.Minute)
	got := f.screens()
	want := []string{"background", "text:FRIGO", "text: ", "text:FRIGO", "text: ", "background"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("screens %v, want %v", got, want)
	}
	if f.read(vfdLevel2) != vfdMessageNone || f.read(vfdLevel3) != vfdLevel3None {
		t.Fatalf("display not handed back: %v", f.mem)
	}
	// 1 s free + 2 x (4.95 s + 1.5 s); scheduling adds a little.
	if took < 13*time.Second || took > 18*time.Second {
		t.Errorf("took %v", took)
	}
}

func TestDisplayStandbyLiftsAndRestoresBlankScreen(t *testing.T) {
	f, m, stop := startDisplay(t, vfdLevel3StandbyBlank)
	defer stop()
	show(t, m, `{"text":"frigo","repeat":2}`)
	waitIdle(t, f, m, time.Minute)
	got := f.screens()
	// A blank between passes, none at the end: the screen goes black at once.
	want := []string{"black", "text:FRIGO", "text: ", "text:FRIGO", "black"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("screens %v, want %v", got, want)
	}
	if f.read(vfdLevel3) != vfdLevel3StandbyBlank || f.read(vfdLevel2) != vfdMessageNone {
		t.Fatalf("standby screen not restored: %v", f.mem)
	}
}

func TestDisplayWaitsForTheBarThenShows(t *testing.T) {
	f, m, stop := startDisplay(t, vfdLevel3None)
	defer stop()
	f.set(vfdLevel2, 0) // VOL on screen
	show(t, m, `{"text":"frigo"}`)
	time.Sleep(3 * time.Second / displayScale)
	if f.read(vfdLevel2) != 0 {
		t.Fatal("covered the bar's message")
	}
	f.set(vfdLevel2, vfdMessageNone)
	waitIdle(t, f, m, time.Minute)
	if count(f.screens(), "text:FRIGO") != 1 {
		t.Fatalf("screens %v", f.screens())
	}
}

func TestDisplayWakeUpReplaysTheInterruptedPass(t *testing.T) {
	f, m, stop := startDisplay(t, vfdLevel3StandbyBlank)
	defer stop()
	show(t, m, `{"text":"frigo","repeat":2}`)
	waitScreen(t, f, "text:FRIGO", 10*time.Second)
	time.Sleep(2 * time.Second / displayScale)
	f.set(vfdLevel3, 22) // HELLO
	time.Sleep(3 * time.Second / displayScale)
	f.set(vfdLevel3, vfdLevel3None)
	time.Sleep(400 * time.Millisecond / displayScale)
	f.set(vfdLevel2, 28) // SMART MODE OFF
	time.Sleep(2 * time.Second / displayScale)
	f.set(vfdLevel2, vfdMessageNone)
	waitIdle(t, f, m, time.Minute)
	got := f.screens()
	// Interrupted pass, then two complete passes; the bar is on at the end: blank.
	if n := count(got, "text:FRIGO"); n != 3 {
		t.Fatalf("FRIGO shown %d times: %v", n, got)
	}
	if got[len(got)-2] != "text: " || got[len(got)-1] != "background" {
		t.Fatalf("end %v", got)
	}
	if f.read(vfdLevel3) != vfdLevel3None {
		t.Fatalf("the standby screen came back after the wake-up: %v", f.mem)
	}
}

func TestDisplaySameIDReplaces(t *testing.T) {
	f, m, stop := startDisplay(t, vfdLevel3None)
	defer stop()
	show(t, m, `{"text":"frigo ouvert","id":"frigo","forever":true}`)
	waitScreen(t, f, "text:FRIGO OUVERT", 10*time.Second)
	time.Sleep(2 * time.Second / displayScale)
	show(t, m, `{"text":"frigo fermé","id":"frigo"}`)
	waitIdle(t, f, m, time.Minute)
	got := f.screens()
	if count(got, "text:FRIGO FERME") != 1 || got[len(got)-1] != "background" {
		t.Fatalf("screens %v", got)
	}
	last := -1
	for i, s := range got {
		if s == "text:FRIGO OUVERT" {
			last = i
		}
	}
	for _, s := range got[last+1:] {
		if s == "text:FRIGO OUVERT" {
			t.Fatalf("the replaced message came back: %v", got)
		}
	}
}

func TestDisplayCancel(t *testing.T) {
	f, m, stop := startDisplay(t, vfdLevel3StandbyBlank)
	defer stop()
	show(t, m, `{"text":"porte","id":"door","forever":true}`)
	waitScreen(t, f, "text:PORTE", 10*time.Second)
	if !m.Cancel("door") {
		t.Fatal("nothing cancelled")
	}
	waitIdle(t, f, m, 10*time.Second)
	if f.read(vfdLevel3) != vfdLevel3StandbyBlank || f.read(vfdLevel2) != vfdMessageNone {
		t.Fatalf("not handed back: %v", f.mem)
	}
	if m.Cancel("door") || m.Cancel("") {
		t.Fatal("cancelled something that was not running")
	}
}

func TestDisplayEndlessLetsQueuedMessagesThrough(t *testing.T) {
	f, m, stop := startDisplay(t, vfdLevel3None)
	defer stop()
	show(t, m, `{"text":"frigo ouvert","id":"frigo","forever":true}`)
	waitScreen(t, f, "text:FRIGO OUVERT", 10*time.Second)
	show(t, m, `{"text":"colis","id":"parcel"}`)
	waitScreen(t, f, "text:COLIS", 30*time.Second)
	waitScreen(t, f, "text:FRIGO OUVERT", 30*time.Second)
	m.Cancel("frigo")
	waitIdle(t, f, m, 30*time.Second)
	if n := count(f.screens(), "text:COLIS"); n != 1 {
		t.Fatalf("COLIS shown %d times: %v", n, f.screens())
	}
}

func TestDisplayDurationFinishesTheLastPass(t *testing.T) {
	f, m, stop := startDisplay(t, vfdLevel3None)
	defer stop()
	show(t, m, `{"text":"frigo ouvert","duration_s":10}`)
	took := waitIdle(t, f, m, time.Minute)
	got := f.screens()
	// 6.6 s passes: the second starts before 10 s and is shown to its end.
	if n := count(got, "text:FRIGO OUVERT"); n != 2 {
		t.Fatalf("passes %d: %v", n, got)
	}
	if took < 16*time.Second {
		t.Errorf("the last pass was cut: %v", took)
	}
}

func TestDisplayGateBusyDefersStepsAndHandBackIsForced(t *testing.T) {
	f, m, stop := startDisplay(t, vfdLevel3StandbyBlank)
	defer stop()
	show(t, m, `{"text":"porte","forever":true}`)
	waitScreen(t, f, "text:PORTE", 10*time.Second)
	f.mu.Lock()
	f.gateOff = true // a long control holds the gate
	f.mu.Unlock()
	m.Cancel("")
	waitIdle(t, f, m, 30*time.Second)
	if f.read(vfdLevel3) != vfdLevel3StandbyBlank || f.read(vfdLevel2) != vfdMessageNone {
		t.Fatalf("not handed back while the gate was busy: %v", f.mem)
	}
}

func TestDisplayClockMismatchStops(t *testing.T) {
	f, m, stop := startDisplay(t, vfdLevel3None)
	defer stop()
	f.set(vfdTempTick, 0x7fffffff) // the bar's last tick far ahead of ours
	show(t, m, `{"text":"frigo"}`)
	waitIdle(t, f, m, time.Minute)
	if count(f.screens(), "text:FRIGO") != 0 || f.read(vfdLevel2) != vfdMessageNone {
		t.Fatalf("showed with a wrong clock: %v", f.screens())
	}
}

func TestDisplayWritesStayInsideTheMessageFields(t *testing.T) {
	m := barDisplayMem{&device{}}
	for _, a := range []int64{vfdState, vfdOverlay, vfdContext, vfdTickPeriod, 15427784} {
		if err := m.writeDisplay(a, make([]byte, 4)); err == nil || !strings.Contains(err.Error(), "outside") {
			t.Errorf("write at %#x: %v", a, err)
		}
	}
	if err := m.writeDisplay(vfdTempText, make([]byte, 4)); err == nil {
		t.Error("short text write accepted")
	}
}

func TestDisplayMessagesTakeTurns(t *testing.T) {
	f, m, stop := startDisplay(t, vfdLevel3None)
	defer stop()
	show(t, m, `{"text":"hello","id":"a","repeat":3}`)
	waitScreen(t, f, "text:HELLO", 10*time.Second)
	show(t, m, `{"text":"goodbye","id":"b","repeat":2}`)
	waitIdle(t, f, m, 2*time.Minute)
	var order []string
	for _, s := range f.screens() {
		if s == "text:HELLO" || s == "text:GOODBYE" {
			order = append(order, strings.TrimPrefix(s, "text:"))
		}
	}
	// Each keeps its own count; they alternate while both have passes left.
	if got := strings.Join(order, " "); got != "HELLO GOODBYE HELLO GOODBYE HELLO" {
		t.Fatalf("order %q", got)
	}
}
