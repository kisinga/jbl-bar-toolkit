package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"
)

func TestTimerChimeIsAPlayableWAV(t *testing.T) {
	b := timerChime()
	if string(b[0:4]) != "RIFF" || string(b[8:16]) != "WAVEfmt " || string(b[36:40]) != "data" {
		t.Fatalf("header %q", b[:44])
	}
	if rate := binary.LittleEndian.Uint32(b[24:28]); rate != timerChimeRate {
		t.Fatalf("rate %d", rate)
	}
	data := binary.LittleEndian.Uint32(b[40:44])
	if int(data)+44 != len(b) || binary.LittleEndian.Uint32(b[4:8]) != uint32(len(b)-8) {
		t.Fatalf("sizes: data %d, file %d", data, len(b))
	}
	seconds := float64(data) / 2 / timerChimeRate
	if seconds < 2 || seconds > 4 {
		t.Fatalf("lasts %.1f s", seconds)
	}
	peak := 0
	for i := 44; i+1 < len(b); i += 2 {
		v := int(int16(binary.LittleEndian.Uint16(b[i:])))
		if v < 0 {
			v = -v
		}
		peak = max(peak, v)
	}
	if peak < 10000 || peak > 32000 {
		t.Fatalf("peak %d: too quiet or clipping", peak)
	}
}

// fakeReceiver answers like the bar's Cast receiver: LAUNCH, then LOAD.
func fakeReceiver(t *testing.T, conn net.Conn, loadReply string, got chan<- map[string]any) {
	t.Helper()
	// Writes go through a queue: net.Pipe is unbuffered, and the client
	// writes too (PONG) while the receiver has more to say.
	out := make(chan []byte, 16)
	go func() {
		for frame := range out {
			if _, e := conn.Write(frame); e != nil {
				return
			}
		}
	}()
	defer close(out)
	send := func(namespace, payload string) {
		b := encodeCastMessage(castMessage{source: "receiver-0", destination: castSoundSender, namespace: namespace, payload: payload})
		frame := make([]byte, 4, 4+len(b))
		binary.BigEndian.PutUint32(frame, uint32(len(b)))
		out <- append(frame, b...)
	}
	for {
		m, e := readCastMessage(conn)
		if e != nil {
			return
		}
		var body map[string]any
		_ = json.Unmarshal([]byte(m.payload), &body)
		switch body["type"] {
		case "LAUNCH":
			send(castHeartbeat, `{"type":"PING"}`)
			send(castReceiver, `{"type":"RECEIVER_STATUS","requestId":1,"status":{"applications":[{"appId":"CC1AD845","transportId":"web-7","namespaces":[{"name":"urn:x-cast:com.google.cast.media"}]}]}}`)
		case "PONG", "LOAD":
			select { // never block the receiver on a full report channel
			case got <- body:
			default:
			}
		}
		switch body["type"] {
		case "LOAD":
			if loadReply == "fail" {
				send(castMedia, `{"type":"LOAD_FAILED","requestId":2}`)
				continue
			}
			send(castMedia, `{"type":"MEDIA_STATUS","status":[{"mediaSessionId":1,"playerState":"BUFFERING"}]}`)
			send(castMedia, `{"type":"MEDIA_STATUS","status":[{"mediaSessionId":1,"playerState":"PLAYING"}]}`)
			send(castMedia, `{"type":"MEDIA_STATUS","status":[{"mediaSessionId":1,"playerState":"IDLE","idleReason":"FINISHED"}]}`)
		}
	}
}

func TestCastPlayConnection(t *testing.T) {
	for _, c := range []struct {
		reply string
		want  error
	}{{"ok", nil}, {"fail", errCastSoundFailed}} {
		client, receiver := net.Pipe()
		got := make(chan map[string]any, 4)
		go fakeReceiver(t, receiver, c.reply, got)
		_ = client.SetDeadline(time.Now().Add(5 * time.Second))
		launched := false
		err := castPlayConnection(client, "http://127.0.0.1:4567/x.wav", "audio/wav", 0, func() { launched = true })
		client.Close()
		receiver.Close()
		if err != c.want {
			t.Fatalf("%s: %v, want %v", c.reply, err, c.want)
		}
		if !launched {
			t.Fatal("launched not called")
		}
		if pong := <-got; pong["type"] != "PONG" {
			t.Fatalf("heartbeat not answered: %v", pong)
		}
		load := <-got
		media, _ := load["media"].(map[string]any)
		if load["type"] != "LOAD" || load["autoplay"] != true || media["contentId"] != "http://127.0.0.1:4567/x.wav" || media["contentType"] != "audio/wav" {
			t.Fatalf("load %v", load)
		}
	}
}

func TestCastPlayRingsAgainUntilTheTimeIsUp(t *testing.T) {
	client, receiver := net.Pipe()
	got := make(chan map[string]any, 64)
	go fakeReceiver(t, receiver, "ok", got)
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	err := castPlayConnection(client, "http://x/y.wav", "audio/wav", 300*time.Millisecond, nil)
	client.Close()
	receiver.Close()
	if err != nil {
		t.Fatal(err)
	}
	loads := 0
	for len(got) > 0 {
		if (<-got)["type"] == "LOAD" {
			loads++
		}
	}
	// The fake finishes at once: it is loaded again until 300 ms have passed.
	if loads < 2 || time.Since(start) < 300*time.Millisecond {
		t.Fatalf("%d loads in %v", loads, time.Since(start))
	}
}

func TestTimerSoundRules(t *testing.T) {
	for _, c := range []struct {
		power, input string
		watching     bool
		restore      string
	}{
		{"On", "TV / ARC", true, "input_tv"}, {"On", "Optical", true, "input_tv"}, {"On", "HDMI", true, "input_hdmi"},
		{"On", "Bluetooth", false, "input_bluetooth"}, {"On", "Chromecast", false, ""}, {"On", "AirPlay", false, ""},
		{"Standby", "HDMI", false, "input_hdmi"},
	} {
		s := state{Power: c.power, Input: c.input}
		if watchingTV(s) != c.watching || restoreAction(c.input) != c.restore {
			t.Errorf("%s %s: watching %v restore %q", c.power, c.input, watchingTV(s), restoreAction(c.input))
		}
	}
	for _, mode := range []string{"off", "unless_tv", "always"} {
		if !validTimerSound(mode) {
			t.Errorf("%s refused", mode)
		}
	}
	if validTimerSound("loud") || validTimerSound("") {
		t.Error("an unknown mode accepted")
	}
}

func TestTimerPlaysItsSoundWhenItRings(t *testing.T) {
	f, _, tm, stop := startTimers(t, vfdLevel3None)
	defer stop()
	var mu sync.Mutex
	var played []string
	tm.sound = func(ctx context.Context, o timerSoundOptions) {
		mu.Lock()
		played = append(played, o.Mode)
		mu.Unlock()
		if o.Volume != 15 || o.RingFor != 30*time.Second {
			t.Errorf("default options %+v", o)
		}
	}
	startTimer(t, tm, `{"text":"pizza","duration_s":1,"sound":"unless_tv"}`)
	startTimer(t, tm, `{"text":"pates","duration_s":1}`) // no sound
	waitScreen(t, f, "text:TIME'S UP PIZZA", 20*time.Second)
	waitScreen(t, f, "text:TIME'S UP PATES", 30*time.Second)
	mu.Lock()
	defer mu.Unlock()
	if len(played) != 1 || played[0] != "unless_tv" {
		t.Fatalf("sounds %v", played)
	}
	if l := tm.List(); len(l) != 2 || (l[0].Sound != "unless_tv" && l[1].Sound != "unless_tv") {
		t.Fatalf("list %+v", l)
	}
}

func TestTimerSoundParameter(t *testing.T) {
	r, err := parseTimerStart(json.RawMessage(`{"duration_s":60}`))
	if err != nil || r.Sound != "off" || r.SoundVolume != 0 || r.RingFor != 0 {
		t.Fatalf("default: %+v %v", r, err)
	}
	r, err = parseTimerStart(json.RawMessage(`{"duration_s":60,"sound":"always"}`))
	if err != nil || r.Sound != "always" || r.SoundVolume != 15 || r.RingFor != 30*time.Second {
		t.Fatalf("always, defaults: %+v %v", r, err)
	}
	r, err = parseTimerStart(json.RawMessage(`{"duration_s":60,"sound":"unless_tv","sound_volume":22,"ring_s":120}`))
	if err != nil || r.SoundVolume != 22 || r.RingFor != 2*time.Minute {
		t.Fatalf("chosen: %+v %v", r, err)
	}
	for _, in := range []string{`{"duration_s":60,"sound":"loud"}`, `{"duration_s":60,"sound":"always","sound_volume":0}`,
		`{"duration_s":60,"sound":"always","sound_volume":31}`, `{"duration_s":60,"sound":"always","ring_s":2}`, `{"duration_s":60,"sound":"always","ring_s":301}`} {
		if _, err := parseTimerStart(json.RawMessage(in)); err == nil {
			t.Errorf("%s accepted", in)
		}
	}
}
