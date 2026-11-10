package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Timers on the front display (extension bar91.timer). A timer counts down in
// the remote. With the countdown option, the bar shows the time left: when
// it is the only timer running, second by second (4'59, short enough not to
// scroll); when several run, once a minute, each in turn (4 MIN LEFT PIZZA).
// When it ends, TIME'S UP and its name scroll in a loop until a key of the
// bar's remote is pressed (on or in standby), it is cancelled, or 10 minutes
// pass. Qualified on the owner's Bar 9.1 on 10 Nov 2026: the key flag
// (vfdKeyFlag) rises on every key press, also in standby; a short text
// rewritten in place stays still, a longer one scrolls and each rewrite
// restarts it. Timers do not survive a restart.
const (
	timerUnnamedAlarm = "TIME'S UP - PRESS ANY KEY" // a timer without a name
	timerTick         = 250 * time.Millisecond      // countdown display loop
	timerMaxCount     = 8
	timerMaxDuration  = 12 * time.Hour
	timerAlarmFor     = 10 * time.Minute
	timerDisplayID    = "timer-"
)

var errTimerNotFound = errors.New("no such timer")

type barTimer struct {
	ID        string
	Text      string
	Countdown bool
	Deadline  time.Time
	Ringing   bool
	cancel    context.CancelFunc
}

// timerStatus is what the page and Home Assistant see.
type timerStatus struct {
	ID          string `json:"id"`
	Text        string `json:"text"`
	Countdown   bool   `json:"countdown"`
	RemainingMS int64  `json:"remaining_ms"`
	Ringing     bool   `json:"ringing"`
}

type timerManager struct {
	display *displayManager
	port    displayPort
	logf    func(string, ...any)
	mu      sync.Mutex
	root    context.Context
	next    int
	timers  map[string]*barTimer
}

func newTimerManager(display *displayManager, port displayPort, logf func(string, ...any)) *timerManager {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &timerManager{display: display, port: port, logf: logf, timers: map[string]*barTimer{}}
}

// Run serves timers until ctx ends; the display manager stops their messages.
func (m *timerManager) Run(ctx context.Context) {
	m.mu.Lock()
	m.root = ctx
	m.mu.Unlock()
	m.countdowns(ctx)
	m.mu.Lock()
	for _, t := range m.timers {
		t.cancel()
	}
	m.timers = map[string]*barTimer{}
	m.mu.Unlock()
}

// timerClock is the time left as the display reads it well and without
// scrolling: 4'59, 1H04'59 (':' does not show correctly).
func timerClock(left time.Duration) string {
	if left < 0 {
		left = 0
	}
	sec := int((left + time.Second - 1) / time.Second)
	if sec >= 3600 {
		return fmt.Sprintf("%dH%02d'%02d", sec/3600, sec/60%60, sec%60)
	}
	return fmt.Sprintf("%d'%02d", sec/60, sec%60)
}

// timerReminder is the once-a-minute reminder when several timers run, time
// first so that it does not read as the alarm: 4 MIN LEFT PIZZA.
func timerReminder(name string, minutes int) string {
	left := fmt.Sprintf("%d MIN LEFT", minutes)
	if name != "" && len(left)+1+len(name) <= displayTextMax {
		return left + " " + name
	}
	return left
}

// timerAlarmText is long enough to scroll (a short text only blinks between
// passes): TIME'S UP PIZZA, or TIME'S UP - PRESS ANY KEY without a name.
func timerAlarmText(name string) string {
	if name == "" {
		return timerUnnamedAlarm
	}
	if text := "TIME'S UP " + name; len(text) <= displayTextMax {
		return text
	}
	return name
}

type timerRequest struct {
	Text      string
	For       time.Duration
	Countdown bool
}

func parseTimerStart(data json.RawMessage) (timerRequest, error) {
	var p struct {
		Text      string `json:"text"`
		DurationS *int   `json:"duration_s"`
		Countdown bool   `json:"countdown"`
	}
	if err := extensionParameters(data, &p); err != nil {
		return timerRequest{}, err
	}
	if p.DurationS == nil || *p.DurationS < 1 || time.Duration(*p.DurationS)*time.Second > timerMaxDuration {
		return timerRequest{}, errors.New("duration_s: 1 to 43200 (12 h)")
	}
	r := timerRequest{For: time.Duration(*p.DurationS) * time.Second, Countdown: p.Countdown}
	if strings.TrimSpace(p.Text) != "" { // no name: the alarm says TIME'S UP - PRESS ANY KEY
		text, err := displayText(p.Text)
		if err != nil {
			return timerRequest{}, err
		}
		r.Text = text
	}
	return r, nil
}

func parseTimerCancel(data json.RawMessage) (string, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := extensionParameters(data, &p); err != nil {
		return "", err
	}
	if p.ID == "" {
		return "", errors.New("provide the id of the timer")
	}
	return p.ID, nil
}

// Start adds a timer. Two timers cannot share a text: the display would not
// tell them apart.
func (m *timerManager) Start(r timerRequest) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.root == nil || m.root.Err() != nil {
		return "", errors.New("timers are not running")
	}
	for _, t := range m.timers {
		if t.Text == r.Text && r.Text == "" {
			return "", errors.New("a timer without a name is already running; give this one a name")
		}
		if t.Text == r.Text {
			return "", fmt.Errorf("a timer named %s is already running", r.Text)
		}
	}
	if len(m.timers) >= timerMaxCount {
		return "", fmt.Errorf("at most %d timers at a time", timerMaxCount)
	}
	m.next++
	t := &barTimer{ID: fmt.Sprintf("t%d", m.next), Text: r.Text, Countdown: r.Countdown, Deadline: m.port.now().Add(r.For)}
	ctx, cancel := context.WithCancel(m.root)
	t.cancel = cancel
	m.timers[t.ID] = t
	go m.run(ctx, t)
	return t.ID, nil
}

// Cancel stops a timer, running or ringing.
func (m *timerManager) Cancel(id string) error {
	m.mu.Lock()
	t := m.timers[id]
	if t != nil {
		delete(m.timers, id)
		t.cancel()
	}
	m.mu.Unlock()
	if t == nil {
		return errTimerNotFound
	}
	m.display.Cancel(timerDisplayID + id)
	return nil
}

func (m *timerManager) List() []timerStatus {
	now := m.port.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]timerStatus, 0, len(m.timers))
	for _, t := range m.timers {
		left := t.Deadline.Sub(now)
		if left < 0 || t.Ringing {
			left = 0
		}
		out = append(out, timerStatus{ID: t.ID, Text: t.Text, Countdown: t.Countdown, RemainingMS: left.Milliseconds(), Ringing: t.Ringing})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RemainingMS != out[j].RemainingMS {
			return out[i].RemainingMS < out[j].RemainingMS
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (m *timerManager) remove(t *barTimer) {
	m.mu.Lock()
	if m.timers[t.ID] == t {
		delete(m.timers, t.ID)
	}
	m.mu.Unlock()
	t.cancel()
}

func (m *timerManager) run(ctx context.Context, t *barTimer) {
	if m.port.sleep(ctx, t.Deadline.Sub(m.port.now())) != nil {
		return // cancelled
	}
	m.mu.Lock()
	if m.timers[t.ID] != t {
		m.mu.Unlock()
		return
	}
	t.Ringing = true
	m.mu.Unlock()
	// Same display id: the alarm replaces the countdown or a reminder.
	done := func(reason string) {
		if reason != "cancelled" {
			m.logf("Timer %s: alarm ended (%s)", t.Text, reason)
		}
		m.remove(t)
	}
	err := m.display.Show(displayRequest{ID: timerDisplayID + t.ID, Text: timerAlarmText(t.Text), Mode: displayUntil, For: timerAlarmFor, Passes: 1,
		Hooks: &displayHooks{StopOnKey: true, Loop: true, Done: done}})
	if err != nil {
		m.logf("Timer %s: alarm not shown: %v", t.Text, err)
		m.remove(t)
	}
}

// countdowns shows the time left of the timers that asked for it. One timer
// running: its clock, second by second, in place (4'59). Several: once a
// minute, a round where each one with the option shows 4 MIN LEFT PIZZA in
// turn; rounds follow the minutes of the timer that ends first.
func (m *timerManager) countdowns(ctx context.Context) {
	live := ""          // timer whose clock shows
	var round time.Time // next round of reminders (several timers)
	for {
		now := m.port.now()
		m.mu.Lock()
		var running, counting []*barTimer
		for _, t := range m.timers {
			if !t.Ringing {
				running = append(running, t)
				if t.Countdown {
					counting = append(counting, t)
				}
			}
		}
		m.mu.Unlock()
		sort.Slice(counting, func(i, j int) bool { return counting[i].Deadline.Before(counting[j].Deadline) })
		want := ""
		if len(running) == 1 && len(counting) == 1 {
			want = counting[0].ID
		}
		if live != "" && live != want {
			// Its clock stops: still running (another timer started), or ringing
			// or cancelled, whose alarm or cancel already replaced it.
			for _, t := range running {
				if t.ID == live {
					m.display.Cancel(timerDisplayID + live)
				}
			}
			live = ""
		}
		if want != "" && live != want {
			t := counting[0]
			clock := func() string { return timerClock(t.Deadline.Sub(m.port.now())) }
			if err := m.display.Show(displayRequest{ID: timerDisplayID + t.ID, Text: clock(), Mode: displayForever, Passes: 1,
				Hooks: &displayHooks{Live: clock, Loop: true}}); err != nil {
				m.logf("Timer %s: countdown not shown: %v", t.Text, err)
			}
			live = want
		}
		if want == "" && len(running) > 1 && len(counting) > 0 {
			if round.IsZero() || round.Before(now.Add(-time.Minute)) {
				// The next whole minute left of the first timer to end that
				// has a minute or more left.
				round = time.Time{}
				for _, t := range counting {
					if minutes := int((t.Deadline.Sub(now) - time.Nanosecond) / time.Minute); minutes >= 1 {
						round = t.Deadline.Add(-time.Duration(minutes) * time.Minute)
						break
					}
				}
			}
			if !round.IsZero() && !now.Before(round) {
				for _, t := range counting {
					// Nearest minute: at a round, the first timer has just under N min left.
					if minutes := int((t.Deadline.Sub(now) + time.Minute/2) / time.Minute); minutes >= 1 {
						if err := m.display.Show(displayRequest{ID: timerDisplayID + t.ID, Text: timerReminder(t.Text, minutes), Mode: displayPasses, Passes: 1}); err != nil {
							m.logf("Timer %s: reminder not shown: %v", t.Text, err)
						}
					}
				}
				round = round.Add(time.Minute)
			}
		} else {
			round = time.Time{}
		}
		if m.port.sleep(ctx, timerTick) != nil {
			return
		}
	}
}
