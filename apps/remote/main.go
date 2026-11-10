// A small, firmware-pinned LAN remote. It exposes fixed buttons, never a shell.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"jbl-bar-toolkit/remote/internal/extension"
	"jbl-bar-toolkit/remote/internal/server"
	webui "jbl-bar-toolkit/remote/www"
)

type config struct {
	Headless     bool `json:"headless"`
	AutoStandard bool `json:"auto_standard"`
	// SmartModeMemory restores the mode chosen before standby instead of
	// always selecting Standard; it needs AutoStandard.
	SmartModeMemory bool   `json:"smart_mode_memory"`
	Listen          string `json:"listen"`
	AllowedCIDR     string `json:"allowed_cidr"`
}
type bluetoothState struct {
	peer         string // Current peer address stays inside native control code.
	MediaReady   bool   `json:"media_ready"`
	PeerName     string `json:"peer_name,omitempty"`
	Selected     bool   `json:"selected"`
	Name         string `json:"name,omitempty"`
	Connectable  bool   `json:"connectable"`
	Discoverable bool   `json:"discoverable"`
	Connected    *bool  `json:"connected,omitempty"`
	Audio        string `json:"audio,omitempty"`
}
type state struct {
	BassOffset    *bassOffsetState `json:"-"`
	Extensions    map[string]any   `json:"extensions,omitempty"`
	Automation    *automationState `json:"automation,omitempty"`
	EQ            *frontEQState    `json:"eq,omitempty"`
	System        *systemState     `json:"system,omitempty"`
	Hostname      string           `json:"hostname,omitempty"`
	MediaControls []string         `json:"media_controls,omitempty"`
	Revision      uint64           `json:"revision"`
	Sound         *soundState      `json:"sound,omitempty"`
	Setup         *setupState      `json:"setup,omitempty"`
	Control       *server.Control  `json:"control,omitempty"` // server-owned: the control holding the gate
	Streaming     *streamState     `json:"streaming,omitempty"`
	SourceState   *uint32          `json:"source_state,omitempty"`
	WiFi          wifiState        `json:"wifi"`
	Bluetooth     *bluetoothState  `json:"bluetooth,omitempty"`
	Available     bool             `json:"available"`
	Error         string           `json:"error,omitempty"`
	Power         string           `json:"power"`
	Mode          string           `json:"mode"`
	Volume        *uint32          `json:"volume,omitempty"`
	Muted         *bool            `json:"muted,omitempty"`
	Source        *uint32          `json:"source,omitempty"`
	Input         string           `json:"input"`
	Uptime        int64            `json:"uptime"`
	Watcher       string           `json:"watcher"`
	CSRF          string           `json:"csrf"`
}
type device struct {
	// ownKeyUntil (Unix ns): the remote sent a key, which raises the bar's key
	// flag like a press on the JBL remote, until about then.
	ownKeyUntil       atomic.Int64
	automation        *automationState
	automationRunning bool
	// manualModes counts Standard/Smart selections not made by the automation;
	// manualMode is the last one's mode (0 Standard, 3 Smart).
	manualModes uint64
	manualMode  uint32
	eq          eqController
	monitor     systemMonitor
	mu          sync.Mutex
	pid         int
	start       string
	revision    uint64
	cliChecked  bool
	center      centerController
	format      formatTracker
	bass        bassController
}
type remote struct {
	// displayThroughGate lets the display messages run while the timer
	// sound holds the gate for its Cast launch and volume (timer_sound_bar91.go).
	displayThroughGate atomic.Bool
	http               *server.Server[state]
	backend            nativeControls
	extensions         extension.Registry[state]
	actionAliases      map[string]string
	cfg                config
	network            *net.IPNet
	csrf               string
	device             *device
	feed               stateFeed
	stop               <-chan struct{}
	actionMu           sync.Mutex
	display            *displayManager
	displayPort        displayPort
	timers             *timerManager
}

// This native seam preserves existing Bar 9.1 fixtures. The reusable public
// HTTP composition contract is internal/server.Backend[S].
type nativeControls interface {
	snapshot() state
	action(context.Context, string) (state, error)
}

func (r *remote) snapshot() state { return r.decorate(r.backend.snapshot()) }
func (r *remote) decorate(s state) state {
	values := r.extensions.State(s)
	if len(values) != 0 {
		merged := make(map[string]any, len(values)+len(s.Extensions))
		for key, value := range s.Extensions {
			merged[key] = value
		}
		for key, value := range values {
			merged[key] = value
		}
		s.Extensions = merged
	}
	return s
}

func hashFile(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func processStart(pid int) (string, error) {
	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if e != nil {
		return "", e
	}
	i := strings.LastIndexByte(string(b), ')')
	if i < 0 {
		return "", errors.New("invalid process stat")
	}
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 20 {
		return "", errors.New("short process stat")
	}
	return f[19], nil
}
func (d *device) identify() error {
	entries, e := os.ReadDir("/proc")
	if e != nil {
		return e
	}
	pid := 0
	for _, entry := range entries {
		p, e := strconv.Atoi(entry.Name())
		if e != nil {
			continue
		}
		b, e := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
		if e == nil && strings.TrimSpace(string(b)) == "bdpprog" {
			if pid != 0 {
				return errors.New("multiple soundbar processes")
			}
			pid = p
		}
	}
	if pid == 0 {
		return errors.New("soundbar is starting")
	}
	start, e := processStart(pid)
	if e != nil {
		return e
	}
	if d.pid != pid || d.start != start {
		h, e := hashFile(fmt.Sprintf("/proc/%d/exe", pid))
		if e != nil {
			return e
		}
		if h != firmwareHash {
			return errors.New("unsupported firmware; controls disabled")
		}
		d.pid = pid
		d.start = start
	}
	return nil
}
func (d *device) same() bool { s, e := processStart(d.pid); return e == nil && s == d.start }
func (d *device) read(address int64, width int) (uint32, error) {
	f, e := os.Open(fmt.Sprintf("/proc/%d/mem", d.pid))
	if e != nil {
		return 0, e
	}
	defer f.Close()
	b := make([]byte, width)
	if _, e = f.ReadAt(b, address); e != nil {
		return 0, e
	}
	switch width {
	case 1:
		return uint32(b[0]), nil
	case 2:
		return uint32(binary.LittleEndian.Uint16(b)), nil
	case 4:
		return binary.LittleEndian.Uint32(b), nil
	}
	return 0, errors.New("invalid read width")
}
func (d *device) readBytes(address int64, n int) ([]byte, error) {
	f, e := os.Open(fmt.Sprintf("/proc/%d/mem", d.pid))
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b := make([]byte, n)
	if _, e = f.ReadAt(b, address); e != nil {
		return nil, e
	}
	return b, nil
}
func (d *device) write(address int64, width int, value uint32) error {
	if !d.same() {
		return errors.New("soundbar restarted")
	}
	if !(address == 15427780 && width == 1 || address == 15427784 && width == 4) {
		return errors.New("write outside console fields")
	}
	f, e := os.OpenFile(fmt.Sprintf("/proc/%d/mem", d.pid), os.O_WRONLY, 0)
	if e != nil {
		return e
	}
	defer f.Close()
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], value)
	if _, e = f.WriteAt(b[:width], address); e != nil {
		return e
	}
	got, e := d.read(address, width)
	if e != nil {
		return e
	}
	if got != value {
		return errors.New("console write verification failed")
	}
	return nil
}
func (d *device) configByte(id uint32) (uint32, error) {
	switch id {
	case 50, 51, 155, 365, 238, 201, 202, avSyncConfig:
	default:
		return 0, errors.New("unknown setting")
	}
	node := int64(0xf046d0) + int64(id)*0x4c
	t, e := d.read(node+0x28, 4)
	if e != nil {
		return 0, e
	}
	count, e := d.read(node+0x2c, 2)
	if e != nil {
		return 0, e
	}
	enabled, e := d.read(node+0x40, 1)
	if e != nil {
		return 0, e
	}
	if t != 1 || count != 1 || enabled != 1 {
		return 0, errors.New("setting layout changed")
	}
	base, e := d.read(0xd89770, 4)
	if e != nil {
		return 0, e
	}
	offset, e := d.read(node+0x48, 4)
	if e != nil {
		return 0, e
	}
	if base < 0x10000 || offset > 2*1024*1024 || uint64(base)+uint64(offset) > 0xffffffff {
		return 0, errors.New("invalid setting address")
	}
	return d.read(int64(base)+int64(offset), 1)
}

// REA: BT_GAP mode at context+8; the version-pinned context is static.
// 0/2 are discoverable; 1/3 are not. A2DP state is distinct from source selection.
func bluetoothAudio(value uint32) (bool, string, bool) {
	switch value {
	case 0:
		return false, "Disconnected", true
	case 1:
		return true, "Connected", true
	case 2:
		return true, "Paused", true
	case 3:
		return true, "Streaming", true
	}
	return false, "", false
}
func bluetoothName(b []byte) string {
	end := bytes.IndexByte(b, 0)
	if end < 1 || !utf8.Valid(b[:end]) {
		return ""
	}
	for _, r := range string(b[:end]) {
		if r < 32 || r == 127 {
			return ""
		}
	}
	return string(b[:end])
}
func (d *device) bluetooth() *bluetoothState {
	gap, e := d.read(0xc6d250+0x44, 4)
	if e != nil || gap != 0xc6d2c0 {
		return nil
	}
	mode, e := d.read(int64(gap)+8, 4)
	if e != nil || mode > 3 {
		return nil
	}
	s := &bluetoothState{Connectable: mode == 0 || mode == 1, Discoverable: mode == 0 || mode == 2}
	// GAP info's inline local-name buffer is 128 bytes, zero-terminated.
	info, ie := d.read(0xc6d250+0x4c, 4)
	if info == 0xc6d2cc && ie == nil {
		if f, e := os.Open(fmt.Sprintf("/proc/%d/mem", d.pid)); e == nil {
			b := make([]byte, 128)
			if _, e = f.ReadAt(b, int64(gap)+0x34); e == nil {
				s.Name = bluetoothName(b)
			}
			f.Close()
		}
	}
	a2dp, e := d.read(0xcb6450+0x44, 4)
	if e == nil && a2dp == 0xe3757c {
		value, e := d.read(int64(a2dp)+4, 4)
		if e == nil {
			if connected, audio, ok := bluetoothAudio(value); ok {
				s.Connected = &connected
				s.Audio = audio
			}
		}
	}
	if s.Connected != nil && *s.Connected && info == 0xc6d2cc {
		if f, e := os.Open(fmt.Sprintf("/proc/%d/mem", d.pid)); e == nil {
			b := make([]byte, 32)
			if _, e = f.ReadAt(b, int64(info)+0xc8); e == nil {
				address := peerAddress(b)
				name := readPeerName(address)
				again := make([]byte, 32)
				_, e = f.ReadAt(again, int64(info)+0xc8)
				value, se := d.read(0xe3757c+4, 4)
				connected, _, valid := bluetoothAudio(value)
				if e == nil && se == nil && valid && connected && address != "" && peerAddress(again) == address {
					s.PeerName = name
					s.peer = address
					s.MediaReady = d.bluetoothMediaReady(address)
				}
			}
			f.Close()
		}
	}
	return s
}
func (d *device) snapshotLocked() state {
	if e := d.identify(); e != nil {
		d.revision++
		s := state{BassOffset: d.bassStatus(), System: d.monitor.sample(time.Now()), Revision: d.revision, WiFi: readWiFi(), Power: "Starting", Mode: "Unknown", Input: "Unknown", Error: e.Error()}
		if d.automation != nil {
			a := *d.automation
			s.Automation = &a
			s.Watcher = a.Status
		}
		return s
	}
	return d.snapshotCurrent()
}

// During an action keep the original PID/start time; never restore saved
// console fields into a replacement firmware process.
func (d *device) snapshotCurrent() state {
	d.revision++
	s := state{BassOffset: d.bassStatus(), System: d.monitor.sample(time.Now()), Revision: d.revision, Power: "Starting", Mode: "Unknown", Input: "Unknown", Hostname: discoveryHostname()}
	if b, e := os.ReadFile("/proc/uptime"); e == nil {
		v, _ := strconv.ParseFloat(strings.Fields(string(b))[0], 64)
		s.Uptime = int64(v)
	}
	if b, e := os.ReadFile("/var/JBLStandard/WATCH_STATUS.txt"); e == nil {
		s.Watcher = strings.TrimSpace(string(b))
	}
	if d.automation != nil {
		a := *d.automation
		s.Automation = &a
		s.Watcher = a.Status
	}
	p, ep := d.read(14191893, 1)
	system, es := d.read(12962676, 4)
	mode, em := d.read(13378532, 4)
	if ep != nil || es != nil || em != nil {
		s.Error = "Soundbar state is temporarily unavailable"
		return s
	}
	s.Available = true
	s.Bluetooth = d.bluetooth()
	sound := d.sound()
	s.Sound = &sound
	s.Setup = d.setup()
	if v, e := d.configByte(365); e == nil {
		s.SourceState = &v
		if s.Bluetooth != nil {
			s.Bluetooth.Selected = v == 2
		}
		s.Input = sourceName(v)
		s.Streaming = d.streaming(v)
	}
	s.WiFi = readWiFi()
	if p == 1 && system == 0 {
		s.Power = "On"
	} else if system == 1 || system == 2 || system == 3 || p == 0 {
		s.Power = "Standby"
	}
	if mode == 0 {
		s.Mode = "Standard"
	} else if mode == 3 {
		s.Mode = "Smart"
	}
	if v, e := d.configByte(50); e == nil {
		s.Volume = &v
	}
	if v, e := d.configByte(155); e == nil {
		b := v != 0
		s.Muted = &b
	}
	if v, e := d.configByte(51); e == nil {
		s.Source = &v
	}
	if s.Bluetooth != nil && s.Bluetooth.Selected {
		s.Input = "Bluetooth"
	}
	if !d.same() {
		s.Available = false
		s.Error = "Soundbar restarted; refreshing"
	}
	if s.Power == "On" {
		s.Sound.AudioFormat = d.audioFormat(s.SourceState)
	}
	s.MediaControls = mediaCommands(s)
	s.EQ = d.frontEQStatus(s)
	return s
}
func (d *device) snapshot() state { d.mu.Lock(); defer d.mu.Unlock(); return d.snapshotLocked() }

// markOwnKey extends the time the remote's own keys may show on the flag.
func (d *device) markOwnKey(hold time.Duration) {
	until := time.Now().Add(hold).UnixNano()
	for {
		old := d.ownKeyUntil.Load()
		if old >= until || d.ownKeyUntil.CompareAndSwap(old, until) {
			return
		}
	}
}

func (d *device) cli(ctx context.Context, args ...string) error {
	if !d.same() {
		return errors.New("soundbar restarted")
	}
	if len(args) > 0 && args[0] == "mw.iom.se" && controlBusy() {
		return errors.New("Smart Mode automation is busy; retry shortly")
	}
	if len(args) > 0 && args[0] == "mw.iom.se" {
		// The key and its hold, plus a margin for the flag to fall.
		hold := 1500 * time.Millisecond
		if len(args) > 2 {
			if ms, e := strconv.Atoi(args[2]); e == nil && ms > 0 {
				hold += time.Duration(ms) * time.Millisecond
			}
		}
		d.markOwnKey(hold)
		defer d.markOwnKey(1500 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/cli", args...)
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "LD_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	output, e := cmd.CombinedOutput()
	if e != nil {
		return fmt.Errorf("local control request failed: %w (%s)", e, strings.TrimSpace(string(output)))
	}
	return nil
}
func (d *device) idleKeys() bool {
	for _, item := range [][2]int64{{13822224, 1}, {14200904, 4}, {14200898, 1}} {
		v, e := d.read(item[0], int(item[1]))
		if e != nil || v != 0 {
			return false
		}
	}
	return true
}
func (d *device) acknowledge(ctx context.Context, wait time.Duration) error {
	dir, e := os.MkdirTemp("/tmp", "jbl-web-ack-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	marker := filepath.Join(dir, "done")
	// Native cli has no completion response. A fixed marker command confirms
	// that the firmware parser consumed the preceding request before restoring.
	if e = d.cli(ctx, "exec", "/bin/busybox", "touch", marker); e != nil {
		return e
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if e := ctx.Err(); e != nil {
			return e
		}
		if _, e = os.Stat(marker); e == nil {
			return nil
		}
		if !d.same() {
			return errors.New("soundbar restarted")
		}
		time.Sleep(25 * time.Millisecond)
	}
	return errors.New("local control was not acknowledged")
}
func actionObserved(action string, before, after state) bool {
	if !after.Available {
		return false
	}
	switch action {
	case "volume_up", "volume_down":
		return before.Volume != nil && after.Volume != nil && *before.Volume != *after.Volume
	case "mute":
		return before.Muted != nil && after.Muted != nil && *before.Muted != *after.Muted
	case "power":
		return before.Power == "On" && after.Power == "Standby" || before.Power == "Standby" && after.Power == "On"
	case "input_tv":
		return after.SourceState != nil && *after.SourceState == 0
	case "input_hdmi":
		return after.SourceState != nil && *after.SourceState == 3
	case "input_bluetooth":
		return after.Bluetooth != nil && after.Bluetooth.Selected
	case "bluetooth_pair":
		return after.Bluetooth != nil && after.Bluetooth.Selected && after.Bluetooth.Discoverable && after.Bluetooth.Connectable
	case "bass_next", "atmos_next", "rear_next":
		return soundObserved(action, before.Sound, after.Sound)
	case "night_toggle":
		return before.Sound != nil && after.Sound != nil && before.Sound.Night != nil && after.Sound.Night != nil && *before.Sound.Night != *after.Sound.Night
	case "media_play", "media_pause":
		return mediaObserved(action, after)
	case "media_next", "media_previous", "media_stop":
		// These are requests: no track metadata/stop acknowledgment is guaranteed.
		return before.Input == after.Input
	case "standard":
		return after.Mode == "Standard"
	case "smart":
		return after.Mode == "Smart"
	}
	return false
}

// eqResetBefore reports whether an active Front EQ must be restored before a
// control. Volume, mute and playback leave it in place. Power from standby is
// sent without it: the restore needs the bar on with an audio route, so it
// would fail and block the only control that can wake the bar; the EQ monitor
// restores the tuning once the bar is on.
func eqResetBefore(action string, on bool) bool {
	switch {
	case action == "mute" || action == "volume_up" || action == "volume_down" || strings.HasPrefix(action, "media_"):
		return false
	case eqFollowsNatively(action):
		// The firmware keeps, recomputes or drops the EQ itself (eq_keep.go).
		return false
	case action == "power" && !on:
		return false
	}
	return true
}
func controlBusy() bool {
	if _, e := os.Stat("/tmp/jbl-standard-hook-active"); e == nil {
		return true
	}
	b, e := os.ReadFile("/var/JBLStandard/WATCH_STATUS.txt")
	return e == nil && (strings.Contains(string(b), "APPLYING") || strings.Contains(string(b), "STABILIZING"))
}
func (d *device) action(ctx context.Context, action string) (out state, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	keys := map[string]string{"volume_up": "0x6f000", "volume_down": "0x6f001", "mute": "0x6f002", "power": "0x8f000", "input_tv": "0x1f00c", "input_hdmi": "0x5f03e", "input_bluetooth": "0x6f027", "bluetooth_pair": "0x6f027", "bass_next": "0x1f030", "atmos_next": "0x1f02f", "rear_next": "0x1f031", "media_play": "0x9f000", "media_pause": "0x9f001", "media_stop": "0x9f002", "media_next": "0x9f005", "media_previous": "0x9f006"}
	_, simple := keys[action]
	if !simple && action != "standard" && action != "smart" && action != "night_toggle" {
		return out, errors.New("unknown control")
	}
	if eqResetBefore(action, d.powerOn()) {
		if e := d.resetFrontEQBefore(ctx, action); e != nil {
			return d.snapshotCurrent(), e
		}
	}
	before := d.snapshotLocked()
	if !before.Available {
		return before, errors.New(before.Error)
	}
	if strings.HasPrefix(action, "media_") && !mediaAllowed(action, before) {
		return before, errors.New("this source does not report support for that playback control")
	}
	if action == "night_toggle" && (before.Sound == nil || before.Sound.Night == nil) {
		return before, errors.New("night mode status unavailable")
	}
	if action == "bass_next" || action == "atmos_next" || action == "rear_next" {
		if _, _, ok := soundValue(action, before.Sound); !ok {
			return before, errors.New("sound setting unavailable")
		}
		if action == "rear_next" && (before.Sound.RearDocked == nil || *before.Sound.RearDocked) {
			return before, errors.New("undock both rear speakers first")
		}
	}
	if action == "bluetooth_pair" {
		if before.Bluetooth == nil || !before.Bluetooth.Selected {
			return before, errors.New("select Bluetooth first; pairing status must be available")
		}
		if before.Bluetooth.Discoverable && before.Bluetooth.Connectable {
			return before, nil
		}
	}
	if action == "standard" && before.Mode == "Standard" || action == "smart" && before.Mode == "Smart" {
		return before, nil
	}
	if action != "power" && before.Power != "On" {
		return before, errors.New("turn the bar on first")
	}
	if action == "power" && before.Power != "On" && before.Power != "Standby" {
		return before, errors.New("soundbar is starting; wait before using power")
	}
	if _, e := os.Stat("/tmp/jbl-standard-hook-active"); e == nil {
		return before, errors.New("Smart Mode automation is busy; try again shortly")
	}
	if !d.automationRunning && (strings.Contains(before.Watcher, "APPLYING") || strings.Contains(before.Watcher, "STABILIZING")) {
		return before, errors.New("sound settings are settling; try again shortly")
	}
	if !d.idleKeys() {
		return before, errors.New("remote control is busy; release its buttons and retry")
	}
	if controlBusy() {
		return before, errors.New("Smart Mode automation is busy; retry shortly")
	}
	if (action == "volume_up" || action == "volume_down" || action == "mute") && !d.eq.active && castVolumeReady(before) {
		return d.castVolumeAction(ctx, action, before)
	}
	if strings.HasPrefix(action, "media_") && castMediaReady(before) {
		return d.castMediaAction(ctx, action, before)
	}
	if strings.HasPrefix(action, "media_") && before.SourceState != nil && *before.SourceState == 2 {
		return d.bluetoothAction(ctx, action, before)
	}
	if !d.cliChecked {
		h, e := hashFile("/bin/cli")
		if e != nil || h != cliHash {
			return before, errors.New("unsupported local control client")
		}
		d.cliChecked = true
	}
	input, e := d.read(15427780, 1)
	if e != nil {
		return before, e
	}
	gate, e := d.read(15427784, 4)
	if e != nil {
		return before, e
	}
	if !(gate == 2 && (input == 0 || input == 1)) {
		return before, errors.New("local console is in use; retry shortly")
	}
	if controlBusy() {
		return before, errors.New("Smart Mode automation is busy; retry shortly")
	}
	keySent := false
	airplay := strings.HasPrefix(action, "media_") && airplayReady(before)
	// Restore both fields even if a command fails or the request disconnects.
	defer func() {
		if keySent && (action == "standard" || action == "smart" || action == "night_toggle") {
			deadline := time.Now().Add(5 * time.Second)
			for d.same() && !d.idleKeys() && time.Now().Before(deadline) {
				time.Sleep(50 * time.Millisecond)
			}
			if d.same() && !d.idleKeys() {
				err = errors.New("key release failed; stop controls and collect logs")
			}
		}
		a := d.write(15427784, 4, gate)
		b := d.write(15427780, 1, input)
		if a != nil || b != nil {
			err = errors.New("console restoration failed; stop controls and collect logs")
		} else if err != nil && (action == "standard" || action == "smart") && keySent && before.Muted != nil && d.same() && d.idleKeys() {
			c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if e := d.eqMute(c, *before.Muted); e != nil {
				err = errors.New("mute restoration failed; stop controls and collect logs")
			}
		}
	}()
	if e = d.write(15427780, 1, 1); e != nil {
		return before, e
	}
	if e = d.write(15427784, 4, 0); e != nil {
		return before, e
	}
	if e = d.cli(ctx, ""); e != nil {
		return before, e
	}
	if e = d.acknowledge(ctx, 3*time.Second); e != nil {
		return before, fmt.Errorf("console readiness: %w", e)
	}
	if strings.HasPrefix(action, "media_") && !mediaAllowed(action, d.snapshotCurrent()) {
		return d.snapshotCurrent(), errors.New("playback source or capability changed; refresh before retrying")
	}
	if action == "standard" || action == "smart" || action == "night_toggle" {
		if action == "standard" && before.Mode != "Smart" || action == "smart" && before.Mode != "Standard" {
			return before, errors.New("unknown sound mode")
		}
		if !d.awakeForGesture(action, before.SourceState) {
			return before, errors.New("bar is no longer ready for the sound gesture")
		}
		keySent = true
		if e = d.cli(ctx, "mw.iom.se", "0x6f002", "3500"); e != nil {
			return before, e
		}
		deadline := time.Now().Add(5 * time.Second)
		ready := false
		for time.Now().Before(deadline) {
			if e := ctx.Err(); e != nil {
				return before, e
			}
			combo, _ := d.read(14200904, 4)
			repeat, _ := d.read(14200898, 1)
			if combo == 1 && repeat == 1 {
				ready = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !ready {
			return before, errors.New("mute hold was not confirmed")
		}
		if !d.awakeForGesture(action, before.SourceState) {
			return before, errors.New("bar left active mode during the sound gesture")
		}
		comboKey := "0x6f000"
		if action == "night_toggle" {
			comboKey = "0x6f001"
		}
		if e = d.cli(ctx, "mw.iom.se", comboKey, "50"); e != nil {
			return before, e
		}
	} else if action == "bluetooth_pair" && before.Source != nil && *before.Source == 10 {
		current := d.snapshotCurrent()
		if current.Power != "On" || current.Source == nil || *current.Source != 10 || current.SourceState == nil || *current.SourceState != 2 {
			return current, errors.New("Bluetooth source changed before pairing; refresh before retrying")
		}
		if e = d.cli(ctx, "app.bt.pair"); e != nil {
			return before, e
		}
	} else if airplay {
		command, ok := airplayCommand(action)
		if !ok {
			return before, errors.New("unsupported AirPlay control")
		}
		if e = d.cli(ctx, command...); e != nil {
			return before, e
		}
	} else {
		duration := "60"
		// Misc repeat callback: 400 ms initial delay, 200 ms repeats, 25 to pair.
		if action == "bluetooth_pair" {
			duration = "6000"
		}
		if e = d.cli(ctx, "mw.iom.se", keys[action], duration); e != nil {
			return before, e
		}
	}
	ackWait := 3 * time.Second
	if action == "bluetooth_pair" {
		ackWait = 9 * time.Second
	}
	if e = d.acknowledge(ctx, ackWait); e != nil {
		return before, fmt.Errorf("control completion: %w", e)
	}
	wait := 3 * time.Second
	if action == "bluetooth_pair" {
		wait = 9 * time.Second
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if e := ctx.Err(); e != nil {
			return before, e
		}
		if !d.same() {
			return before, errors.New("soundbar restarted during the request")
		}
		time.Sleep(100 * time.Millisecond)
		if d.idleKeys() {
			out = d.snapshotCurrent()
			if airplay {
				if !airplaySettingsPreserved(before, out) {
					return out, errors.New("AirPlay settings changed during the request; check playback before retrying")
				}
				// SDK acceptance is a request, not sender playback feedback.
				return out, nil
			}
			if actionObserved(action, before, out) {
				if (action == "standard" || action == "smart") && !standardSettingsPreserved(before, out) {
					return out, errors.New("Sound mode changed but volume or mute changed; automatic retry stopped")
				}
				if (action == "standard" || action == "smart") && !d.automationRunning {
					d.manualModes++
					d.manualMode = 0
					if action == "smart" {
						d.manualMode = 3
					}
				}
				return out, nil
			}
		}
	}
	return d.snapshotCurrent(), errors.New("change not confirmed (or volume limit reached); refresh before retrying")
}
func newRemote(cfg config) (*remote, error) {
	if cfg.Headless && !cfg.AutoStandard {
		return nil, errors.New("headless mode requires auto_standard")
	}
	if cfg.SmartModeMemory && !cfg.AutoStandard {
		return nil, errors.New("smart_mode_memory requires auto_standard")
	}
	_, network, e := net.ParseCIDR(cfg.AllowedCIDR)
	if e != nil {
		return nil, e
	}
	r := &remote{cfg: cfg, network: network, device: &device{}}
	r.backend = r.device
	if err := r.registerBar91Extensions(); err != nil {
		return nil, err
	}
	r.feed.decorate = r.decorate
	r.http, e = server.New[state](bar91Backend{r}, server.Options[state]{AllowedCIDR: cfg.AllowedCIDR, Hostname: "jbl.local", Assets: webui.Files, Gate: &r.actionMu, Feed: &r.feed.Feed, ActionTimeout: bar91ActionTimeout, Hotspot: hotspotNetwork, Done: func() <-chan struct{} { return r.stop }})
	if e != nil {
		return nil, e
	}
	r.csrf = r.http.Token()
	return r, nil
}
func (r *remote) ServeHTTP(w http.ResponseWriter, q *http.Request) { r.http.ServeHTTP(w, q) }
func main() {
	path := flag.String("config", "/var/JBLRemote/config.json", "private configuration file")
	readyFile := flag.String("ready-file", "", "optional startup receipt for the local installer")
	inspect := flag.Bool("inspect-device", false, "print a read-only native installation check and exit")
	publish := flag.Bool("publish-directory", false, "installer-only atomic no-replace publication; requires SOURCE TARGET")
	requireAwake := flag.Bool("require-awake", false, "require normal awake native state during installation inspection")
	restoreSmart := flag.Bool("restore-smart-helper", false, "restore only this remote's paused legacy Smart Mode helper")
	flag.Parse()
	if *publish {
		if *inspect || *restoreSmart || len(flag.Args()) != 2 {
			log.Fatal("publication requires only SOURCE TARGET")
		}
		if err := publishDirectory(flag.Arg(0), flag.Arg(1)); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *inspect {
		result, err := inspectInstallation(*requireAwake)
		if err != nil {
			result.Error = err.Error()
		}
		json.NewEncoder(os.Stdout).Encode(result)
		if err != nil {
			os.Exit(1)
		}
		return
	}
	if *restoreSmart {
		if e := restoreSmartHelperWithLock(); e != nil {
			log.Fatal(e)
		}
		return
	}
	b, e := os.ReadFile(*path)
	if e != nil {
		log.Fatal(e)
	}
	var cfg config
	if e = json.Unmarshal(b, &cfg); e != nil {
		log.Fatal(e)
	}
	r, e := newRemote(cfg)
	if e != nil {
		log.Fatal(e)
	}
	server := &http.Server{Addr: cfg.Listen, Handler: r, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	markReady := func() error {
		if *readyFile == "" {
			return nil
		}
		return os.WriteFile(*readyFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0600)
	}
	if *readyFile != "" {
		defer os.Remove(*readyFile)
	}
	if cfg.Headless {
		// Automation alone opens no HTTP listener or discovery registration.
		if e := markReady(); e != nil {
			log.Print(e)
			return
		}
		runtime := r.startBar91(ctx)
		defer runtime.Close()
		<-runtime.automationDone
		return
	}
	r.stop = ctx.Done()
	go r.http.Run(ctx)
	stopped := make(chan struct{})
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 17*time.Second)
		defer cancel()
		server.Shutdown(c)
		close(stopped)
	}()
	addresses := []string{cfg.Listen}
	if cfg.Listen == ":80" {
		addresses = append(addresses, ":8080")
	} else if cfg.Listen == ":8080" {
		addresses = append(addresses, ":80")
	}
	var listeners []net.Listener
	for _, address := range addresses {
		listener, e := net.Listen("tcp", address)
		if e != nil {
			log.Printf("Listener unavailable on %s: %v", address, e)
			continue
		}
		listeners = append(listeners, listener)
		log.Printf("JBL remote listening on %s", address)
	}
	if len(listeners) == 0 {
		log.Print("No remote listener available")
		return
	}
	runtime := r.startBar91(ctx)
	defer runtime.Close()
	if e := markReady(); e != nil {
		log.Print(e)
		return
	}
	finished := make(chan error, len(listeners))
	for _, listener := range listeners {
		go func(l net.Listener) { finished <- server.Serve(l) }(listener)
	}
	for range listeners {
		if e := <-finished; e != nil && e != http.ErrServerClosed {
			log.Printf("Listener stopped: %v", e)
		}
	}
	if ctx.Err() != nil {
		<-stopped
	}
}
