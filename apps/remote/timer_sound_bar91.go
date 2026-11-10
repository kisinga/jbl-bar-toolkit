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
	timerSoundBudget  = 45 * time.Second // connect, launch, fetch, play
	timerSoundRestore = 20 * time.Second // wait for the gate to put things back
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

// playTimerSound plays the chime through the bar's Cast receiver, then puts
// the bar back as it was: standby, or its previous source.
func (r *remote) playTimerSound(ctx context.Context, mode string) {
	if r.device == nil || mode == timerSoundOff || !timerSoundBusy.TryLock() {
		return
	}
	defer timerSoundBusy.Unlock()
	before := r.device.snapshot()
	if !before.Available {
		return
	}
	if mode == timerSoundUnlessTV && watchingTV(before) {
		displayLog("Timer sound: not played, the bar is on %s", before.Input)
		return
	}
	if err := castPlayServed(ctx, timerChime(), "audio/wav"); err != nil {
		displayLog("Timer sound: %v", err)
	}
	// The bar now stays on Chromecast (and on, if it was in standby).
	action := restoreAction(before.Input)
	if before.Power == "Standby" {
		action = "power"
	} else if before.Power != "On" {
		action = ""
	}
	if action == "" {
		return
	}
	deadline := time.Now().Add(timerSoundRestore)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		release, report, ok := r.hold("Timer sound")
		if !ok {
			time.Sleep(time.Second)
			continue
		}
		now := r.device.snapshot()
		var err error
		switch {
		case action == "power" && now.Power == "On":
			report("Returning to standby after the timer sound")
			_, err = r.device.action(ctx, "power")
		case action != "power" && now.Power == "On" && now.Input != before.Input:
			report("Returning to " + before.Input + " after the timer sound")
			_, err = r.device.action(ctx, action)
		}
		release()
		if err != nil {
			displayLog("Timer sound: could not put the bar back (%s): %v", action, err)
		}
		return
	}
	displayLog("Timer sound: the bar was busy; it stays on Chromecast")
}

// castPlayServed serves sound on a short-lived local address with a random
// path and asks the bar's Cast receiver to play it, trying the loopback
// address first, then the bar's network address.
func castPlayServed(parent context.Context, sound []byte, contentType string) error {
	ctx, cancel := context.WithTimeout(parent, timerSoundBudget)
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
		last = castPlayOnce(ctx, fmt.Sprintf("http://%s:%d%s", host, port, path), contentType)
		if last != errCastSoundFailed {
			return last
		}
	}
	return last
}

func castPlayOnce(ctx context.Context, url, contentType string) error {
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
	return castPlayConnection(conn, url, contentType)
}
