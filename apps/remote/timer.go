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
// the remote, optionally showing the time left; when it ends, its text shows
// until a key of the bar's remote is pressed (on or in standby), it is
// cancelled, or 10 minutes pass. Qualified on the owner's Bar 9.1 on
// 10 Nov 2026: a short text rewritten in place does not scroll or restart;
// the display shows 20 characters; the key flag (vfdKeyFlag) rises on every
// key press, also in standby. Timers do not survive a restart.
const (
	timerDefaultText = "TIMER!"
	timerMaxCount    = 8
	timerMaxDuration = 12 * time.Hour
	timerAlarmFor    = 10 * time.Minute
	timerScreenWidth = 20 // characters shown without scrolling
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

// timerClock writes the time left as the display reads it well: 4'59, 1H04'59.
func timerClock(left time.Duration) string {
	if left < 0 {
		left = 0
	}
	s := int((left + time.Second - 1) / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%dH%02d'%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d'%02d", s/60, s%60)
}

// countdownText puts the name in front of the time left when both fit on the
// display without scrolling.
func countdownText(name string, left time.Duration) string {
	clock := timerClock(left)
	if len(name)+1+len(clock) <= timerScreenWidth {
		return name + " " + clock
	}
	return clock
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
	if t.Countdown {
		live := func() string { return countdownText(t.Text, t.Deadline.Sub(m.port.now())) }
		err := m.display.Show(displayRequest{ID: displayID, Text: live(), Mode: displayForever, Passes: 1, Hooks: &displayHooks{Live: live}})
		if err != nil {
			m.logf("Timer %s: countdown not shown: %v", t.Text, err)
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
	// Same id: the alarm replaces the countdown in its place.
	done := func(reason string) {
		if reason != "cancelled" {
			m.logf("Timer %s: alarm ended (%s)", t.Text, reason)
		}
		m.remove(t)
	}
	err := m.display.Show(displayRequest{ID: displayID, Text: t.Text, Mode: displayUntil, For: timerAlarmFor, Passes: 1,
		Hooks: &displayHooks{StopOnKey: true, Done: done}})
	if err != nil {
		m.logf("Timer %s: alarm not shown: %v", t.Text, err)
		m.remove(t)
	}
}
