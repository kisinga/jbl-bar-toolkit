package main

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Custom text on the front display (bdpprog 23.06.41.80, qualified on the bar
// on 8 Nov 2026). bdpprog shows its own temporary message 0x5b by writing the
// text to vfdContext+0x98, the start tick to vfdContext+0x8c and level-2 slot 1
// to vfdState+0x1c (consumer 0x625b86). The level-2 renderer (0x62260e) keeps
// it while (tick - start) * ms_per_tick < length * 550 ms (length < 6: 9), then
// selects none (117) again. The compare is unsigned: a wrong tick ends the
// message at once, it cannot stick. Tick = CLOCK_MONOTONIC / ms_per_tick, with
// ms_per_tick = 1000 / CLK_TCK stored at 0xcf1854 (10 on the bar).
//
// The remote does the same writes and keeps the start tick fresh, so it decides
// when a pass ends. Level 3 is drawn before level 2: in standby it holds 28, a
// blank screen the display module sets ~1.7 s after STDBY (nothing outside the
// module reads it). It is lifted to 52 (none) while a message shows in standby
// and put back afterwards.
const (
	vfdOverlay            = vfdState + 0x10
	vfdLevel3             = vfdState + 0x18
	vfdLevel2             = vfdState + 0x1c
	vfdTempTick           = vfdContext + 0x8c
	vfdTempText           = vfdContext + 0x98
	vfdTickPeriod         = 0xcf1854
	vfdLevel3None         = 52
	vfdLevel3StandbyBlank = 28
	vfdMessageCustom      = 1
	vfdTextSize           = 32

	displayTextMax  = 31
	displayGap      = 1500 * time.Millisecond // blank between passes, and at the end when the bar is on
	displayPoll     = 200 * time.Millisecond
	displayFreeFor  = 5 // polls the display must stay free (1 s): HELLO, then SMART MODE OFF
	displayWait     = 60 * time.Second
	displayMaxRuns  = 99
	displayMaxTime  = 24 * time.Hour
	displayReleaseT = 5 * time.Second
)

var (
	displayIDPattern   = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)
	displayTextPattern = regexp.MustCompile(`^[A-Z0-9 .:!?'-]{1,31}$`)
	errDisplayGate     = errors.New("display step deferred: another control holds the gate")
	errDisplayStopped  = errors.New("display message stopped")
	errDisplayBusy     = errors.New("display still busy")
	errDisplayDeferred = errors.New("the bar showed its own message")
	// The display has no lower case or accents: fold common Latin letters.
	displayFold = strings.NewReplacer(
		"À", "A", "Â", "A", "Ä", "A", "Á", "A", "Ã", "A", "Å", "A", "Æ", "AE",
		"Ç", "C", "É", "E", "È", "E", "Ê", "E", "Ë", "E",
		"Î", "I", "Ï", "I", "Í", "I", "Ì", "I", "Ñ", "N",
		"Ô", "O", "Ö", "O", "Ó", "O", "Ò", "O", "Õ", "O", "Ø", "O", "Œ", "OE",
		"Û", "U", "Ü", "U", "Ú", "U", "Ù", "U", "Ÿ", "Y", "Ý", "Y", "ß", "SS",
		"’", "'", "‘", "'", "–", "-", "—", "-",
	)
)

// displayText upper-cases and folds a message to what the display can show.
func displayText(raw string) (string, error) {
	text := strings.TrimSpace(displayFold.Replace(strings.ToUpper(raw)))
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "", errors.New("provide a text")
	}
	if len(text) > displayTextMax {
		return "", errors.New("text: at most 31 characters")
	}
	if !displayTextPattern.MatchString(text) {
		return "", errors.New("text: letters, digits, space and - . : ! ? ' only")
	}
	return text, nil
}

// displayPassTime is how long the bar itself would show the text (one pass).
func displayPassTime(text string) time.Duration {
	n := len(text)
	if n < 6 {
		n = 9
	}
	return time.Duration(n) * 550 * time.Millisecond
}

type displayMode int

const (
	displayPasses displayMode = iota
	displayForever
	displayUntil
)

// displayRequest is one validated show request.
type displayRequest struct {
	ID, Text string
	Mode     displayMode
	Passes   int
	For      time.Duration
}

func parseDisplayShow(data json.RawMessage) (displayRequest, error) {
	var p struct {
		Text      string `json:"text"`
		ID        string `json:"id"`
		Repeat    *int   `json:"repeat"`
		DurationS *int   `json:"duration_s"`
		Forever   bool   `json:"forever"`
	}
	if err := extensionParameters(data, &p); err != nil {
		return displayRequest{}, err
	}
	text, err := displayText(p.Text)
	if err != nil {
		return displayRequest{}, err
	}
	r := displayRequest{ID: p.ID, Text: text, Mode: displayPasses, Passes: 1}
	if r.ID == "" {
		r.ID = "default"
	}
	if !displayIDPattern.MatchString(r.ID) {
		return displayRequest{}, errors.New("id: 1-32 of a-z 0-9 _ -")
	}
	set := 0
	if p.Repeat != nil {
		set++
		if *p.Repeat < 1 || *p.Repeat > displayMaxRuns {
			return displayRequest{}, errors.New("repeat: 1-99")
		}
		r.Passes = *p.Repeat
	}
	if p.DurationS != nil {
		set++
		r.Mode, r.For = displayUntil, time.Duration(*p.DurationS)*time.Second
		if r.For < time.Second || r.For > displayMaxTime {
			return displayRequest{}, errors.New("duration_s: 1 to 86400")
		}
	}
	if p.Forever {
		set++
		r.Mode = displayForever
	}
	if set > 1 {
		return displayRequest{}, errors.New("choose one of repeat, duration_s or forever")
	}
	return r, nil
}

func parseDisplayCancel(data json.RawMessage) (string, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := extensionParameters(data, &p); err != nil {
		return "", err
	}
	if p.ID != "" && !displayIDPattern.MatchString(p.ID) {
		return "", errors.New("id: 1-32 of a-z 0-9 _ -")
	}
	return p.ID, nil
}

// displayMem is native display access during one step.
type displayMem interface {
	read32(address int64) (uint32, error)
	write32(address int64, value uint32) error
	writeText(text string) error
	// tick is the native tick now (CLOCK_MONOTONIC / ms_per_tick).
	tick() (uint32, error)
}

// displayPort gives exclusive native access for one short step, and the clock.
type displayPort interface {
	// step returns errDisplayGate when another control holds the gate; the
	// caller retries on its next poll.
	step(force bool, f func(displayMem) error) error
	now() time.Time
	sleep(ctx context.Context, d time.Duration) error
}

type displayJob struct {
	req    displayRequest
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	start  time.Time
	pass   int
	own    bool // our text is selected (level 2 = 1)
	stby   bool // we lifted the standby blank screen (level 3 28 -> 52)
}

// displayManager owns the front display for custom messages: one message on
// screen at a time, FIFO for different ids, replacement for the same id. Long
// and endless messages let queued ones through between passes.
type displayManager struct {
	port    displayPort
	mu      sync.Mutex
	root    context.Context
	jobs    map[string]*displayJob
	queue   []*displayJob // waiting for the display, in order
	owner   *displayJob
	changed chan struct{}
	running sync.WaitGroup
	logf    func(string, ...any)
}

func newDisplayManager(port displayPort, logf func(string, ...any)) *displayManager {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &displayManager{port: port, jobs: map[string]*displayJob{}, changed: make(chan struct{}), logf: logf}
}

func (m *displayManager) notifyLocked() { close(m.changed); m.changed = make(chan struct{}) }

// Run serves requests until ctx ends, then stops every message and hands the
// display back.
func (m *displayManager) Run(ctx context.Context) {
	m.mu.Lock()
	m.root = ctx
	m.mu.Unlock()
	<-ctx.Done()
	m.cancelAll()
	m.running.Wait()
}

// Show queues a message; a message with the same id is replaced in its place.
func (m *displayManager) Show(r displayRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.root == nil || m.root.Err() != nil {
		return errors.New("display messages are not running")
	}
	job := &displayJob{req: r, done: make(chan struct{}), start: m.port.now()}
	job.ctx, job.cancel = context.WithCancel(m.root)
	position := len(m.queue)
	if old := m.jobs[r.ID]; old != nil {
		old.cancel()
		position = 0 // the replaced one was showing: next in line
		for i, q := range m.queue {
			if q == old {
				position = i
				m.queue = append(m.queue[:i], m.queue[i+1:]...)
				break
			}
		}
	}
	m.queue = append(m.queue[:position], append([]*displayJob{job}, m.queue[position:]...)...)
	m.jobs[r.ID] = job
	m.notifyLocked()
	m.running.Add(1)
	go m.run(job)
	return nil
}

// Cancel stops the message with id, or every message when id is empty.
// It reports whether something was stopped.
func (m *displayManager) Cancel(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	found := false
	for key, job := range m.jobs {
		if id == "" || key == id {
			job.cancel()
			found = true
		}
	}
	return found
}

func (m *displayManager) cancelAll() { m.Cancel("") }

// acquire waits until job owns the display, or until the deadline (zero: none).
func (m *displayManager) acquire(job *displayJob, deadline time.Time) error {
	for {
		m.mu.Lock()
		if m.owner == nil && len(m.queue) > 0 && m.queue[0] == job {
			m.owner, m.queue = job, m.queue[1:]
			m.mu.Unlock()
			return nil
		}
		changed := m.changed
		m.mu.Unlock()
		if !deadline.IsZero() && !m.port.now().Before(deadline) {
			return errDisplayBusy
		}
		select {
		case <-job.ctx.Done():
			return errDisplayStopped
		case <-changed:
		case <-time.After(displayPoll):
		}
	}
}

// yield puts job back in line behind the waiting messages.
func (m *displayManager) yield(job *displayJob) {
	m.mu.Lock()
	if m.owner == job {
		m.owner = nil
	}
	m.queue = append(m.queue, job)
	m.notifyLocked()
	m.mu.Unlock()
}

func (m *displayManager) waiting() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.queue) > 0
}

func (m *displayManager) finish(job *displayJob) {
	m.release(job)
	m.mu.Lock()
	if m.owner == job {
		m.owner = nil
	}
	for i, q := range m.queue {
		if q == job {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			break
		}
	}
	if m.jobs[job.req.ID] == job {
		delete(m.jobs, job.req.ID)
	}
	m.notifyLocked()
	m.mu.Unlock()
	job.cancel()
	close(job.done)
	m.running.Done()
}

// release hands the display back: the standby screen first (if still lifted),
// then the level-2 selector. It retries while another control holds the gate
// and finally writes without it, so a message never outlives its job.
func (m *displayManager) release(job *displayJob) {
	if !job.own && !job.stby {
		return
	}
	deadline := m.port.now().Add(displayReleaseT)
	for {
		force := !m.port.now().Before(deadline)
		err := m.port.step(force, func(mem displayMem) error {
			l3, e := mem.read32(vfdLevel3)
			if e != nil {
				return e
			}
			if job.stby && l3 == vfdLevel3None {
				if e = mem.write32(vfdLevel3, vfdLevel3StandbyBlank); e != nil {
					return e
				}
			}
			if job.own {
				if sel, e := mem.read32(vfdLevel2); e == nil && sel == vfdMessageCustom {
					if e = mem.write32(vfdLevel2, vfdMessageNone); e != nil {
						return e
					}
				}
			}
			return nil
		})
		if err != errDisplayGate || force {
			if err != nil {
				m.logf("Display message %s: hand-back failed: %v", job.req.ID, err)
			}
			job.own, job.stby = false, false
			return
		}
		time.Sleep(displayPoll / 4)
	}
}

// free: no native level-2 message, no overlay, no level-3 screen but the
// blank standby one.
func displayFree(mem displayMem) (bool, error) {
	sel, e := mem.read32(vfdLevel2)
	if e != nil {
		return false, e
	}
	overlay, e := mem.read32(vfdOverlay)
	if e != nil {
		return false, e
	}
	l3, e := mem.read32(vfdLevel3)
	if e != nil {
		return false, e
	}
	return sel == vfdMessageNone && overlay == vfdOverlayNone && (l3 == vfdLevel3None || l3 == vfdLevel3StandbyBlank), nil
}

func (m *displayManager) budget(job *displayJob) time.Time {
	switch job.req.Mode {
	case displayForever:
		return time.Time{}
	case displayUntil:
		return job.start.Add(job.req.For)
	}
	return m.port.now().Add(displayWait)
}

func (m *displayManager) more(job *displayJob) bool {
	switch job.req.Mode {
	case displayForever:
		return true
	case displayUntil:
		// No pass starts after the end; a started pass is shown to its end.
		return m.port.now().Before(job.start.Add(job.req.For))
	}
	return job.pass < job.req.Passes
}

// waitFree waits until the display has been free for displayFreeFor polls.
func (m *displayManager) waitFree(job *displayJob) error {
	deadline := m.budget(job)
	free := 0
	for free < displayFreeFor {
		if !deadline.IsZero() && !m.port.now().Before(deadline) {
			return errDisplayBusy
		}
		err := m.port.step(false, func(mem displayMem) error {
			ok, e := displayFree(mem)
			if e == nil {
				if ok {
					free++
				} else {
					free = 0
				}
			}
			return e
		})
		if err != nil && err != errDisplayGate {
			return err
		}
		if m.port.sleep(job.ctx, displayPoll) != nil {
			return errDisplayStopped
		}
	}
	return nil
}

func (m *displayManager) take(job *displayJob, text string) error {
	return m.port.step(false, func(mem displayMem) error {
		if e := mem.writeText(text); e != nil {
			return e
		}
		t, e := mem.tick()
		if e != nil {
			return e
		}
		// The last start tick (the bar's or ours) lies in the past. If it is
		// ahead of our clock, our tick is wrong and every pass would end at once.
		last, e := mem.read32(vfdTempTick)
		if e != nil {
			return e
		}
		if int32(last-t) > 100 {
			return errors.New("display clock does not match the bar")
		}
		if e = mem.write32(vfdTempTick, t); e != nil {
			return e
		}
		if e = mem.write32(vfdLevel2, vfdMessageCustom); e != nil {
			return e
		}
		job.own = true
		l3, e := mem.read32(vfdLevel3)
		if e != nil {
			return e
		}
		if l3 == vfdLevel3StandbyBlank {
			if e = mem.write32(vfdLevel3, vfdLevel3None); e != nil {
				return e
			}
			job.stby = true
		}
		return nil
	})
}

// show keeps text selected for d. It returns errDisplayDeferred when the bar
// showed something of its own: at level 2 it replaces ours, at level 3
// (HELLO, STDBY, a source change) it is drawn over ours.
func (m *displayManager) show(job *displayJob, text string, d time.Duration) error {
	for {
		err := m.take(job, text)
		if err == nil {
			break
		}
		if err != errDisplayGate {
			return err
		}
		if m.port.sleep(job.ctx, displayPoll) != nil {
			return errDisplayStopped
		}
	}
	end := m.port.now().Add(d)
	for m.port.now().Before(end) {
		if m.port.sleep(job.ctx, displayPoll) != nil {
			return errDisplayStopped
		}
		var deferred bool
		err := m.port.step(false, func(mem displayMem) error {
			sel, e := mem.read32(vfdLevel2)
			if e != nil {
				return e
			}
			l3, e := mem.read32(vfdLevel3)
			if e != nil {
				return e
			}
			if sel == vfdMessageCustom && l3 != vfdLevel3None {
				// The bar's level-3 screen is shown now: step back; the
				// standby screen is the bar's again, so it is not put back.
				deferred, job.own, job.stby = true, false, false
				return mem.write32(vfdLevel2, vfdMessageNone)
			}
			if sel != vfdMessageCustom {
				deferred = true
				if job.stby && l3 == vfdLevel3None {
					// Put the standby screen back under the bar's message.
					e = mem.write32(vfdLevel3, vfdLevel3StandbyBlank)
				}
				job.own, job.stby = false, false
				return e
			}
			t, e := mem.tick()
			if e != nil {
				return e
			}
			return mem.write32(vfdTempTick, t)
		})
		if err != nil && err != errDisplayGate {
			return err
		}
		if deferred {
			return errDisplayDeferred
		}
	}
	return nil
}

func (m *displayManager) run(job *displayJob) {
	defer m.finish(job)
	r := job.req
	if err := m.acquire(job, m.budget(job)); err != nil {
		if err != errDisplayStopped {
			m.logf("Display message %s: not shown, another message kept the display", r.ID)
		}
		return
	}
	if err := m.waitFree(job); err != nil {
		if err != errDisplayStopped {
			m.logf("Display message %s: not shown, the display stayed busy", r.ID)
		}
		return
	}
	passTime := displayPassTime(r.Text)
	for m.more(job) {
		err := m.show(job, r.Text, passTime)
		if err == nil {
			job.pass++
			if !m.more(job) {
				// After the last pass, a blank only when the bar is on: in
				// standby the screen goes back to black anyway.
				if !job.stby {
					err = m.show(job, " ", displayGap)
				}
				if err == nil || err == errDisplayDeferred {
					return
				}
			} else if err = m.show(job, " ", displayGap); err == nil {
				if m.waiting() {
					// A long or endless message lets queued messages through.
					m.release(job)
					m.yield(job)
					if err = m.acquire(job, m.budget(job)); err == nil {
						err = m.waitFree(job)
					}
					if err != nil {
						return
					}
				}
				continue
			}
		}
		if err != errDisplayDeferred {
			if err != errDisplayStopped {
				m.logf("Display message %s: stopped: %v", r.ID, err)
			}
			return
		}
		// The bar showed something of its own: let it finish, then show the
		// interrupted pass again (a pass only counts when shown to the end).
		m.release(job)
		if err = m.waitFree(job); err != nil {
			return
		}
	}
}
