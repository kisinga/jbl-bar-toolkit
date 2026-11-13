package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
	_ "time/tzdata" // the bar has no zoneinfo: the time zone comes with the remote
)

// Clock on the front display.
//
// When the owner turns it on, the display shows the time (18 54: the display
// has no colon; a space, so that no segment stays lit all the time and wears
// faster than the others) when nothing else wants the screen: in standby only
// (when "standby", the default), or in standby and with the bar on ("always"),
// where it takes the place of the bar's idle screen (the source's name).
// It is the lowest priority there is:
//   - it only takes the display when no message or timer of ours is showing or
//     waiting (it is not one of the messages that take turns), and the display
//     has been free for a second, like a message;
//   - it steps back as soon as a message or a timer arrives, or the bar shows
//     anything of its own (a level-2 message such as the volume, HELLO at
//     wake-up, STDBY);
//   - with the bar on and when "standby", it never shows: the bar's idle
//     screen stays.
//
// It uses the same four native fields as the messages (display_message.go) and
// gives the standby screen back the same way. The setting (on/off, when, and
// the time zone, an IANA name such as Europe/Paris sent by the page or Home
// Assistant) is kept in clockPath, so it survives a restart.

const (
	clockPath   = "/var/JBLRemote/display-clock.json"
	clockFormat = "15 04"
)

// Where the clock shows when on.
const (
	clockStandby = "standby" // only while the bar is in standby
	clockAlways  = "always"  // in standby and with the bar on
)

type clockSettings struct {
	Enabled bool   `json:"enabled"`
	When    string `json:"when,omitempty"` // clockStandby (also when empty) or clockAlways
	TZ      string `json:"tz"`
}

// clockSet is a change: nil fields keep the current value.
type clockSet struct {
	Enabled *bool   `json:"enabled"`
	When    *string `json:"when"`
	TZ      *string `json:"tz"`
}

type displayClock struct {
	m    *displayManager
	port displayPort
	path string
	logf func(string, ...any)

	mu       sync.Mutex
	settings clockSettings
	loc      *time.Location
	changed  chan struct{}

	// Run goroutine only.
	hold  displayJob // own / stby as for a message; req.ID "clock" for logs
	free  int
	shown string
}

func newDisplayClock(m *displayManager, port displayPort, path string, logf func(string, ...any)) *displayClock {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	c := &displayClock{m: m, port: port, path: path, logf: logf, loc: time.UTC, changed: make(chan struct{}, 1)}
	c.hold.req.ID = "clock"
	if b, err := os.ReadFile(path); err == nil {
		var s clockSettings
		if json.Unmarshal(b, &s) == nil {
			if loc, err := clockLocation(s.TZ); err == nil {
				c.settings, c.loc = s, loc
			}
		}
	}
	return c
}

func clockLocation(tz string) (*time.Location, error) {
	if tz == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("tz: unknown time zone %q", tz)
	}
	return loc, nil
}

func parseClockSet(data json.RawMessage) (clockSet, error) {
	var p clockSet
	if err := extensionParameters(data, &p); err != nil {
		return p, err
	}
	if p.Enabled == nil && p.When == nil {
		return p, errors.New("enabled (true or false) or when (standby or always)")
	}
	if p.When != nil && *p.When != clockStandby && *p.When != clockAlways {
		return p, errors.New("when: standby or always")
	}
	if p.TZ != nil {
		if _, err := clockLocation(*p.TZ); err != nil {
			return p, err
		}
	}
	return p, nil
}

// Set changes the clock: on or off, where it shows, the time zone.
func (c *displayClock) Set(p clockSet) error {
	c.mu.Lock()
	s := c.settings
	if p.Enabled != nil {
		s.Enabled = *p.Enabled
	}
	if p.When != nil {
		s.When = *p.When
	}
	if p.TZ != nil {
		s.TZ = *p.TZ
	}
	loc, err := clockLocation(s.TZ)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	b, _ := json.Marshal(s)
	c.mu.Unlock()
	if c.path != "" {
		if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
			return err
		}
		tmp := c.path + ".tmp"
		if err := os.WriteFile(tmp, b, 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, c.path); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.settings, c.loc = s, loc
	c.mu.Unlock()
	select {
	case c.changed <- struct{}{}:
	default:
	}
	return nil
}

// State is the clock's part of the remote state.
func (c *displayClock) State() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	when := c.settings.When
	if when == "" {
		when = clockStandby
	}
	return map[string]any{"enabled": c.settings.Enabled, "when": when, "tz": c.settings.TZ}
}

func (c *displayClock) now() (enabled, always bool, text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.settings.Enabled, c.settings.When == clockAlways, c.port.now().In(c.loc).Format(clockFormat)
}

// Run keeps the clock on screen when it may be, until ctx ends.
func (c *displayClock) Run(ctx context.Context) {
	defer c.m.release(&c.hold)
	for {
		c.tick()
		select {
		case <-ctx.Done():
			return
		case <-c.changed:
		case <-c.m.changedChan():
		case <-time.After(displayPoll / clockPollDivisor):
		}
	}
}

// clockPollDivisor is 1 on the bar; tests speed the clock's polls up.
var clockPollDivisor time.Duration = 1

func (c *displayClock) tick() {
	enabled, always, text := c.now()
	holding := c.hold.own || c.hold.stby
	if !enabled || !c.m.idle() {
		// Off, or a message or timer of ours wants the screen: give it back now.
		c.free = 0
		if holding {
			c.m.release(&c.hold)
			c.shown = ""
		}
		return
	}
	err := c.port.step(false, func(mem displayMem) error {
		sel, e := mem.read32(vfdLevel2)
		if e != nil {
			return e
		}
		l3, e := mem.read32(vfdLevel3)
		if e != nil {
			return e
		}
		if holding {
			switch {
			case sel == vfdMessageCustom && l3 != vfdLevel3None:
				// HELLO at wake-up, STDBY…: the bar's level-3 screen is shown;
				// the standby screen is the bar's again.
				c.hold.own, c.hold.stby, c.free, c.shown = false, false, 0, ""
				return mem.write32(vfdLevel2, vfdMessageNone)
			case sel != vfdMessageCustom:
				// The bar's own level-2 message replaced ours.
				if c.hold.stby && l3 == vfdLevel3None {
					e = mem.write32(vfdLevel3, vfdLevel3StandbyBlank)
				}
				c.hold.own, c.hold.stby, c.free, c.shown = false, false, 0, ""
				return e
			}
			if !c.hold.stby && !always {
				// Shown with the bar on, now "standby" only: the idle screen is the bar's.
				c.hold.own, c.free, c.shown = false, 0, ""
				return mem.write32(vfdLevel2, vfdMessageNone)
			}
			if text != c.shown {
				if e = mem.writeText(text); e != nil {
					return e
				}
				c.shown = text
			}
			t, e := mem.tick()
			if e != nil {
				return e
			}
			return mem.write32(vfdTempTick, t)
		}
		// Not shown: in standby (the blank screen), or with the bar on (its
		// idle screen) when "always", once the display has been free for
		// displayFreeFor polls.
		ok, e := displayFree(mem)
		if e != nil {
			return e
		}
		standby := l3 == vfdLevel3StandbyBlank
		if !ok || !(standby || always && l3 == vfdLevel3None) {
			c.free = 0
			return nil
		}
		if c.free++; c.free < displayFreeFor {
			return nil
		}
		if e = mem.writeText(text); e != nil {
			return e
		}
		t, e := mem.tick()
		if e != nil {
			return e
		}
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
		c.hold.own, c.shown = true, text
		if !standby {
			return nil // the bar is on: its idle screen gives way to level 2
		}
		if e = mem.write32(vfdLevel3, vfdLevel3None); e != nil {
			return e
		}
		c.hold.stby = true
		return nil
	})
	if err != nil && err != errDisplayGate {
		c.logf("Display clock: %v", err)
	}
}
