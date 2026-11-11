package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	timerSoundSetup    = 30 * time.Second // connect, launch, wake, volume, fetch (plus the ringing time)
	timerSoundRestore  = 25 * time.Second // the gate, volume, mute, then the source
	timerSoundGateWait = 10 * time.Second // another control running when a timer rings
)

// timerSoundBusy plays one sound at a time: timers ending together ring once.
var timerSoundBusy sync.Mutex

// watchingTV: the bar is on with a TV or HDMI source (a film may be playing).
func watchingTV(s state) bool {
	return s.Power == "On" && (s.Input == "TV / ARC" || s.Input == "Optical" || s.Input == "HDMI")
}

// restoreAction is the control that brings the bar back to its source.
func restoreAction(input string) string {
	switch input {
	case "TV / ARC", "Optical":
		return "input_tv"
	case "HDMI":
		return "input_hdmi"
	case "Bluetooth":
		return "input_bluetooth"
	}
	return "" // Chromecast, AirPlay…: nothing to put back
}

// playTimerSound rings the chime through the bar's Cast receiver until
// o.RingFor has passed or ctx ends (the timer was stopped), then puts the bar
// back as it was: volume and mute, then standby or its previous source.
func (r *remote) playTimerSound(ctx context.Context, o timerSoundOptions) {
	if r.device == nil || o.Mode == timerSoundOff || !timerSoundBusy.TryLock() {
		return
	}
	defer timerSoundBusy.Unlock()
	before := r.device.snapshot()
	if !before.Available {
		return
	}
	if o.Mode == timerSoundUnlessTV && watchingTV(before) {
		displayLog("Timer sound: not played, the bar is on %s", before.Input)
		return
	}
	// The volume is raised beside the playback, so that the Cast connection
	// keeps answering heartbeats; the first ring may start a moment quieter.
	// The changes to the bar (the Cast launch, which switches the source and
	// wakes it, then the volume) run under the action gate, like any control,
	// so that they do not interleave with an EQ or another control. The gate
	// is released once they are done: the chime then rings without it, so the
	// alarm on the display and the keys that stop it keep working, and Stop
	// (the timer's ctx) never waits for the gate.
	release, report, ok := r.holdFor(ctx, "Timer sound", timerSoundGateWait)
	if !ok {
		if ctx.Err() == nil {
			displayLog("Timer sound: not played, another control kept the bar busy")
		}
		return
	}
	report("Playing the timer sound")
	if before.Power == "Standby" {
		// Only for the chime: the bar goes back to standby after it, so the
		// wake automation (Standard or the remembered mode) stays out of it.
		r.timerWake.Store(true)
		defer r.timerWake.Store(false)
	}
	// The display and its key watch keep running meanwhile (display_native.go).
	r.displayThroughGate.Store(true)
	var free sync.Once
	unhold := func() {
		free.Do(func() {
			r.displayThroughGate.Store(false)
			release()
		})
	}
	defer unhold()
	var volume volumeChange
	// After the launch, before the chime: from standby, the bar is waking up
	// now; the volume is set first, so the chime rings at it from the start
	// and a key during the sound stops the alarm (owner's choice).
	prepare := func() {
		defer unhold()
		r.waitPowerOn(ctx, 6*time.Second)
		volume = r.barVolume().raise(ctx, o.Volume)
	}
	err := castPlayServed(ctx, timerChime(), "audio/wav", o.RingFor, prepare)
	unhold() // the launch failed before the volume: nothing else to change
	if err != nil && ctx.Err() == nil {
		displayLog("Timer sound: %v", err)
	}
	// Put things back even when the timer was stopped (ctx ended).
	action := restoreAction(before.Input)
	if before.Power == "Standby" {
		action = "power"
	} else if before.Power != "On" {
		action = ""
	}
	if volume == (volumeChange{}) && action == "" {
		return
	}
	restore, cancel := context.WithTimeout(context.Background(), timerSoundRestore)
	defer cancel()
	ops := barRestoreOps{
		hold: func(ctx context.Context) (func(), func(string), bool) {
			return r.holdFor(ctx, "Timer sound", time.Until(deadlineOf(ctx)))
		},
		volume:   func(ctx context.Context) { r.barVolume().restore(ctx, volume) },
		snapshot: r.device.snapshot,
		action:   func(ctx context.Context, a string) error { _, e := r.device.action(ctx, a); return e },
		sleep:    func(ctx context.Context, d time.Duration) { sleepCtx(ctx, d) },
	}
	if !ops.restore(restore, action, before.Input) {
		displayLog("Timer sound: the bar could not be put back (%s); it stays as it is", action)
	}
}

func deadlineOf(ctx context.Context) time.Time {
	if d, ok := ctx.Deadline(); ok {
		return d
	}
	return time.Now().Add(timerSoundRestore)
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// barRestoreOps puts the bar back after the timer sound; tests replace its ends.
type barRestoreOps struct {
	hold     func(context.Context) (release func(), report func(string), ok bool)
	volume   func(context.Context)
	snapshot func() state
	action   func(context.Context, string) error
	sleep    func(context.Context, time.Duration)
}

// timerRestoreSettle: after a key the bar needs a few seconds to report its
// new power or source; power toggles, so it is never pressed again sooner.
const timerRestoreSettle = 6 * time.Second

// restore puts the volume back, then standby (action "power") or the source,
// under the gate each time. The bar woken by the chime is still waking up:
// a key can be refused (the wake automation, "busy"), or not taken; so it
// checks the result and tries again until ctx ends. It reports success.
func (o barRestoreOps) restore(ctx context.Context, action, input string) bool {
	volumeDone := false
	for ctx.Err() == nil {
		release, report, ok := o.hold(ctx)
		if !ok {
			return false
		}
		if !volumeDone {
			report("Putting the bar back after the timer sound")
			o.volume(ctx)
			volumeDone = true
		}
		now := o.snapshot()
		var key string
		// Only from the chime's own state: a source the owner picked during
		// the chime (to watch TV, say) is theirs, and the bar stays on it.
		ours := now.Input == "Chromecast" || now.Input == input
		switch {
		case !ours:
		case action == "power" && now.Power == "On":
			report("Returning to standby after the timer sound")
			key = "power"
		case action != "" && action != "power" && now.Power == "On" && now.Input != input:
			report("Returning to " + input + " after the timer sound")
			key = action
		}
		if key == "" {
			release()
			return true // back as it was (or the bar went off meanwhile)
		}
		err := o.action(ctx, key)
		release()
		if err != nil {
			displayLog("Timer sound: putting the bar back (%s): %v; trying again", key, err)
			o.sleep(ctx, time.Second)
			continue
		}
		o.sleep(ctx, timerRestoreSettle) // then check, and press again only if needed
	}
	return false
}

// holdFor takes the action gate, waiting up to limit while another control
// runs; it gives up when ctx ends.
func (r *remote) holdFor(ctx context.Context, action string, limit time.Duration) (func(), func(string), bool) {
	end := time.Now().Add(limit)
	for ctx.Err() == nil && time.Now().Before(end) {
		if release, report, ok := r.hold(action); ok {
			return release, report, true
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	return nil, nil, false
}

func (r *remote) waitPowerOn(ctx context.Context, limit time.Duration) {
	end := time.Now().Add(limit)
	for time.Now().Before(end) && ctx.Err() == nil && r.device.snapshot().Power != "On" {
		time.Sleep(200 * time.Millisecond)
	}
}

// volumeChange records what raiseVolume changed, to put it back.
type volumeChange struct {
	volume  *int // the volume before, when it was raised
	unmuted bool // the bar was muted
}

// volumeOps changes the bar's volume through the Cast receiver (master
// volume) and reads it back natively; tests replace both ends.
type volumeOps struct {
	snapshot func() state
	control  func(context.Context, castControlRequest) (castControlResult, error)
	settle   time.Duration // before the readback
}

func (r *remote) barVolume() volumeOps {
	return volumeOps{snapshot: r.device.snapshot, control: castControl, settle: 600 * time.Millisecond}
}

// raise unmutes the bar and raises its volume to at least minimum; it never
// lowers it.
func (v volumeOps) raise(ctx context.Context, minimum int) volumeChange {
	var change volumeChange
	s := v.snapshot()
	// What is put back is recorded before each change: a change that fails
	// half-way, or is stopped (a key, Stop), may still have moved the bar.
	if s.Muted != nil && *s.Muted {
		change.unmuted = true
		f := false
		if _, err := v.control(ctx, castControlRequest{Action: "mute", Muted: &f}); err != nil {
			displayLog("Timer sound: could not unmute: %v", err)
		}
	}
	if s.Volume != nil && int(*s.Volume) < minimum {
		was := int(*s.Volume)
		change.volume = &was
		v.set(ctx, minimum)
	}
	return change
}

func (v volumeOps) restore(ctx context.Context, change volumeChange) {
	if change.volume != nil {
		v.set(ctx, *change.volume)
	}
	if change.unmuted {
		t := true
		if _, err := v.control(ctx, castControlRequest{Action: "mute", Muted: &t}); err != nil {
			displayLog("Timer sound: could not mute again: %v", err)
		}
	}
}

// set sets the bar's volume through the Cast receiver and checks it on the
// native side, correcting the Cast level by a step or two.
func (v volumeOps) set(ctx context.Context, target int) bool {
	level := float64(target) / castVolumeSteps
	for try := 0; try < 3 && ctx.Err() == nil; try++ {
		l := min(max(level, 0), 1)
		if _, err := v.control(ctx, castControlRequest{Action: "volume", Level: &l}); err != nil {
			displayLog("Timer sound: volume: %v", err)
			return false
		}
		time.Sleep(v.settle)
		s := v.snapshot()
		if s.Volume == nil {
			return false
		}
		got := int(*s.Volume)
		if got == target {
			return true
		}
		level += float64(target-got) / castVolumeSteps
	}
	return false
}

// castPlayServed serves sound on a short-lived local address with a random
// path and asks the bar's Cast receiver to play it, trying the loopback
// address first, then the bar's network address.
func castPlayServed(parent context.Context, sound []byte, contentType string, ringFor time.Duration, prepare func()) error {
	ctx, cancel := context.WithTimeout(parent, timerSoundSetup+ringFor)
	defer cancel()
	token := make([]byte, 16)
	if _, e := rand.Read(token); e != nil {
		return e
	}
	path := "/" + hex.EncodeToString(token) + ".wav"
	listener, e := net.Listen("tcp", ":0")
	if e != nil {
		return e
	}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if q.URL.Path != path || (q.Method != http.MethodGet && q.Method != http.MethodHead) {
			http.NotFound(w, q)
			return
		}
		w.Header().Set("Content-Type", contentType)
		http.ServeContent(w, q, "chime.wav", time.Time{}, bytesReader(sound))
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	hosts := []string{"127.0.0.1"}
	if ip := readWiFi().IP; ip != "" {
		hosts = append(hosts, ip)
	}
	var last error
	for _, host := range hosts {
		last = castPlayOnce(ctx, fmt.Sprintf("http://%s:%d%s", host, port, path), contentType, ringFor, prepare)
		prepare = nil // the volume is already set
		if last != errCastSoundFailed {
			return last
		}
	}
	return last
}

func castPlayOnce(ctx context.Context, url, contentType string, ringFor time.Duration, prepare func()) error {
	raw, e := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", "127.0.0.1:8009")
	if e != nil {
		return fmt.Errorf("Cast receiver unavailable: %w", e)
	}
	defer raw.Close()
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(deadline)
	}
	conn := tls.Client(raw, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}) // fixed loopback, self-signed
	if e := conn.HandshakeContext(ctx); e != nil {
		return fmt.Errorf("Cast TLS: %w", e)
	}
	return castPlayConnection(conn, url, contentType, ringFor, prepare)
}
