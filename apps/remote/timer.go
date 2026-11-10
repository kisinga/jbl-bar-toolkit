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
// the remote and, on request, shows the minutes left once a minute
// (4 MIN, one pass). When it ends, TIME'S UP and its text scroll in a loop
// until a key of the bar's remote is pressed (on or in standby), it is
// cancelled, or 10 minutes pass. Qualified on the owner's Bar 9.1 on 10 Nov 2026: the key flag
// (vfdKeyFlag) rises on every key press, also in standby. A countdown
// rewritten every second does not work: a text longer than the display
// scrolls and each rewrite restarts it. Timers do not survive a restart.
const (
	timerDefaultText = "TIMER"
	timerMaxCount    = 8
	timerMaxDuration = 12 * time.Hour
	timerAlarmFor    = 10 * time.Minute
	timerDisplayID   = "timer-"
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
	<-ctx.Done()
	m.mu.Lock()
	for _, t := range m.timers {
		t.cancel()
	}
	m.timers = map[string]*barTimer{}
	m.mu.Unlock()
}

// timerReminder is the once-a-minute reminder, time first so that it does not
// read as the alarm: 4 MIN with one timer running, 4 MIN LEFT PATES with
// several (owner's choice, 10 Nov 2026).
func timerReminder(name string, minutes int, several bool) string {
	left := fmt.Sprintf("%d MIN", minutes)
	if several && len(left)+len(" LEFT ")+len(name) <= displayTextMax {
		return left + " LEFT " + name
	}
	return left
}

// timerAlarmText is long enough to scroll on the display (a short text only
// blinks between passes): TIME'S UP PATES.
func timerAlarmText(name string) string {
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
	raw := p.Text
	if strings.TrimSpace(raw) == "" {
		raw = timerDefaultText
	}
	text, err := displayText(raw)
	if err != nil {
		return timerRequest{}, err
	}
	return timerRequest{Text: text, For: time.Duration(*p.DurationS) * time.Second, Countdown: p.Countdown}, nil
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
	displayID := timerDisplayID + t.ID
	// Reminders: one pass at each whole minute left (none at the start or
	// in the last minute). The same display id replaces an unfinished one.
	for t.Countdown {
		minutes := int((t.Deadline.Sub(m.port.now()) - time.Nanosecond) / time.Minute)
		if minutes < 1 {
			break
		}
		if m.port.sleep(ctx, t.Deadline.Add(-time.Duration(minutes)*time.Minute).Sub(m.port.now())) != nil {
			return // cancelled
		}
		m.mu.Lock()
		several := len(m.timers) > 1
		m.mu.Unlock()
		if err := m.display.Show(displayRequest{ID: displayID, Text: timerReminder(t.Text, minutes, several), Mode: displayPasses, Passes: 1}); err != nil {
			m.logf("Timer %s: reminder not shown: %v", t.Text, err)
		}
	}
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
	// Same id: the alarm replaces a reminder still showing.
	done := func(reason string) {
		if reason != "cancelled" {
			m.logf("Timer %s: alarm ended (%s)", t.Text, reason)
		}
		m.remove(t)
	}
	err := m.display.Show(displayRequest{ID: displayID, Text: timerAlarmText(t.Text), Mode: displayUntil, For: timerAlarmFor, Passes: 1,
		Hooks: &displayHooks{StopOnKey: true, Loop: true, Done: done}})
	if err != nil {
		m.logf("Timer %s: alarm not shown: %v", t.Text, err)
		m.remove(t)
	}
}
