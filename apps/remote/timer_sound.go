package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"net"
)

// Sound at the end of a timer. The remote asks the bar's own Cast receiver
// (127.0.0.1:8009) to play a chime it serves itself, as a phone would cast a
// sound. Qualified on the owner's Bar 9.1 on 10 Nov 2026 with catt: the bar
// plays it on or from standby (it wakes up, without waking the TV), at the
// bar's volume, and then stays on the Chromecast source; so the remote puts
// the previous source or standby back afterwards (timer_sound_bar91.go).
const (
	timerSoundOff      = "off"
	timerSoundUnlessTV = "unless_tv" // not when the bar is on TV, optical or HDMI
	timerSoundAlways   = "always"

	castDefaultReceiver = "CC1AD845" // Cast Default Media Receiver
	castSoundSender     = "jbl-remote-sound"
	timerChimeRate      = 22050
)

var errCastSoundFailed = errors.New("the Cast receiver could not play the sound")

func validTimerSound(mode string) bool {
	return mode == timerSoundOff || mode == timerSoundUnlessTV || mode == timerSoundAlways
}

// timerChime is a "dring dring" bell, twice, as a 16-bit mono WAV (~2.8 s):
// two bell partials with a fast tremolo, like an old ringing phone.
func timerChime() []byte {
	var samples []int16
	tone := func(seconds float64) {
		n := int(seconds * timerChimeRate)
		for i := 0; i < n; i++ {
			t := float64(i) / timerChimeRate
			env := math.Min(1, t/0.005) * math.Min(1, (seconds-t)/0.04)
			tremolo := 0.6 + 0.4*math.Copysign(1, math.Sin(2*math.Pi*24*t))
			v := (math.Sin(2*math.Pi*1318.5*t) + 0.35*math.Sin(2*math.Pi*2637*t)) / 1.35
			samples = append(samples, int16(0.55*32767*env*tremolo*v))
		}
	}
	silence := func(seconds float64) { samples = append(samples, make([]int16, int(seconds*timerChimeRate))...) }
	for i := 0; i < 2; i++ {
		tone(0.45)
		silence(0.18)
		tone(0.45)
		silence(0.6)
	}
	var b bytes.Buffer
	data := len(samples) * 2
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+data))
	b.WriteString("WAVEfmt ")
	for _, field := range []any{uint32(16), uint16(1), uint16(1), uint32(timerChimeRate), uint32(timerChimeRate * 2), uint16(2), uint16(16)} {
		_ = binary.Write(&b, binary.LittleEndian, field) // PCM, mono, 16-bit
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(data))
	_ = binary.Write(&b, binary.LittleEndian, samples)
	return b.Bytes()
}

// castPlayConnection launches the Default Media Receiver on an open Cast
// connection, loads url and waits until it has played. errCastSoundFailed
// means the receiver refused or could not fetch the media (the caller may try
// another address); the caller bounds the time with the connection deadline.
func castPlayConnection(conn net.Conn, url, contentType string) error {
	connect := map[string]any{"type": "CONNECT", "origin": map[string]any{}}
	if e := sendCastMessageFrom(conn, castSoundSender, castConnection, "receiver-0", connect); e != nil {
		return e
	}
	if e := sendCastMessageFrom(conn, castSoundSender, castReceiver, "receiver-0", map[string]any{"type": "LAUNCH", "appId": castDefaultReceiver, "requestId": 1}); e != nil {
		return e
	}
	// read returns the next message for this sender, answering heartbeats.
	read := func() (castMessage, map[string]any, error) {
		for {
			m, e := readCastMessage(conn)
			if e != nil {
				return m, nil, e
			}
			var body map[string]any
			if json.Unmarshal([]byte(m.payload), &body) != nil {
				continue
			}
			if m.namespace == castHeartbeat && body["type"] == "PING" {
				if e := sendCastMessageFrom(conn, castSoundSender, castHeartbeat, m.source, map[string]any{"type": "PONG"}); e != nil {
					return m, nil, e
				}
				continue
			}
			if m.destination != castSoundSender && m.destination != "*" {
				continue
			}
			return m, body, nil
		}
	}
	transport := ""
	for transport == "" {
		m, body, e := read()
		if e != nil {
			return e
		}
		switch body["type"] {
		case "LAUNCH_ERROR", "INVALID_REQUEST":
			return errCastSoundFailed
		case "RECEIVER_STATUS":
			if m.namespace != castReceiver {
				continue
			}
			var reply castReceiverReply
			if json.Unmarshal([]byte(m.payload), &reply) != nil {
				continue
			}
			for _, app := range reply.Status.Applications {
				if app.AppID == castDefaultReceiver && app.TransportID != "" {
					transport = app.TransportID
				}
			}
		}
	}
	if e := sendCastMessageFrom(conn, castSoundSender, castConnection, transport, connect); e != nil {
		return e
	}
	load := map[string]any{"type": "LOAD", "requestId": 2, "autoplay": true, "currentTime": 0,
		"media": map[string]any{"contentId": url, "contentType": contentType, "streamType": "BUFFERED",
			"metadata": map[string]any{"metadataType": 0, "title": "Timer"}}}
	if e := sendCastMessageFrom(conn, castSoundSender, castMedia, transport, load); e != nil {
		return e
	}
	played := false
	for {
		m, body, e := read()
		if e != nil {
			return e
		}
		if m.namespace != castMedia {
			continue
		}
		switch body["type"] {
		case "LOAD_FAILED", "LOAD_CANCELLED", "INVALID_REQUEST":
			return errCastSoundFailed
		case "MEDIA_STATUS":
			statuses, _ := body["status"].([]any)
			for _, raw := range statuses {
				status, _ := raw.(map[string]any)
				switch status["playerState"] {
				case "PLAYING", "BUFFERING", "PAUSED":
					played = true
				case "IDLE":
					switch status["idleReason"] {
					case "FINISHED":
						return nil
					case "ERROR":
						return errCastSoundFailed
					case "CANCELLED", "INTERRUPTED":
						if played {
							return nil // something else took the receiver
						}
						return errCastSoundFailed
					}
				}
			}
		}
	}
}

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
