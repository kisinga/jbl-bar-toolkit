package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const legacySmartDir = "/var/JBLStandard"
const goSmartMarker = "JBL_WEB_SMART_MODE_OWNER_REV1\n"

var legacySmartManifests = map[string]string{
	"JBL_STANDARD_HOOK_REV5": "214c8b621d92f5d528bf6a256f3e058bcda23438fdc05677ca242ba50ee95c80",
	"JBL_STANDARD_HOOK_REV6": "6c6119de762336efdaadb76202eb38085f0ff6c89393461e983ed33f20ebb91e",
}

// An OS-held lock prevents a second service or recovery command from restoring
// the shell helper while Go still owns it. It releases automatically on a crash.
const goSmartLock = "/tmp/jbl-go-smart.lock"

func lockSmartAutomation(path string) (*os.File, error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("Invalid Go automation lock")
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, errors.New("Another Go service owns Smart Mode automation")
	}
	return f, nil
}
func restoreSmartHelperWithLock() error {
	f, e := lockSmartAutomation(goSmartLock)
	if e != nil {
		return e
	}
	defer f.Close()
	return restoreLegacySmart()
}

type automationState struct {
	Enabled bool   `json:"enabled"`
	Engine  string `json:"engine"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
	// RememberedMode is the mode restored after the next wake when
	// smart_mode_memory is enabled ("Standard" or "Smart").
	RememberedMode string `json:"remembered_mode,omitempty"`
}
type wakeSample struct {
	identity                    string
	power, system, mode, source uint32
	idle                        bool
}

// A wake is handled once. Manual Smart selection is respected until standby or
// a new firmware process. A failed gesture is never repeated in the same wake.
type wakeTracker struct {
	last            wakeSample
	stable          int
	handled, failed bool
	failureReason   string
	// A mode kept after the wake was handled (see settle).
	settledMode  uint32
	settledCount int
	// manual: the owner chose the mode before the automation acted.
	manual bool
	// timer: a timer's chime woke the bar, which goes back to standby after
	// it; no mode is applied or remembered for this wake.
	timer bool
	// requested: a mode the owner selected from the page or Home Assistant
	// (0 Standard, 3 Smart) and that settle has not reported yet.
	requested       bool
	requestedMode   uint32
	settledReported bool
}

func (w *wakeTracker) observe(s wakeSample) (string, bool) { return w.observeFor(s, false) }

// observeFor is observe for one wake. keepSmart (smart_mode_memory, Smart
// remembered) completes the wake without a gesture once it is stable: the bar
// returns to Smart by itself after wake, so no Smart gesture is ever needed.
func (w *wakeTracker) observeFor(s wakeSample, keepSmart bool) (string, bool) {
	if s.identity == "" {
		// A read gap is not a new wake. Keep completion/failure so a transient
		// procfs/config read cannot replay a gesture or override a manual choice.
		w.stable = 0
		return "WAITING_FOR_PROCESS", false
	}
	if s.identity != w.last.identity || s.power != 1 || s.system != 0 {
		*w = wakeTracker{last: s}
		if s.power != 1 || s.system != 0 {
			return "WAITING_FOR_POWER_ON", false
		}
	}
	if w.handled {
		if w.timer {
			return "TIMER_WAKE", false
		}
		if w.manual {
			return "MANUAL_MODE_THIS_WAKE", false
		}
		if keepSmart {
			return "SMART_KEPT_THIS_WAKE", false
		}
		return "STANDARD_THIS_WAKE", false
	}
	if w.failed {
		return "AUTOMATION_FAILED", false
	}
	if (s.mode != 0 && s.mode != 3) || !s.idle {
		w.stable = 0
		w.last = s
		return "WAITING_FOR_READY", false
	}
	if s.identity != w.last.identity || s.mode != w.last.mode || s.source != w.last.source || !w.last.idle {
		w.stable = 0
	}
	w.last = s
	w.stable++
	if s.mode == 0 && w.stable >= 3 {
		w.handled = true
		w.failed = false
		w.failureReason = ""
		return "ALREADY_STANDARD_THIS_WAKE", false
	}
	if keepSmart && w.stable >= 3 {
		w.handled = true
		return "SMART_KEPT_THIS_WAKE", false
	}
	return fmt.Sprintf("AWAKE_STABILIZING_%d", s.mode), s.mode == 3 && w.stable >= 3
}

// chosen completes a wake the automation has not acted on yet: a Standard or
// Smart selection made by the owner (page, Home Assistant) wins over the
// remembered or default mode, and is remembered like any later choice.
func (w *wakeTracker) chosen(mode uint32) {
	if !w.handled && !w.failed {
		w.handled, w.manual = true, true
	}
	w.requested, w.requestedMode = true, mode
}

// A mode is remembered once it has stayed this many one-second samples:
//   - settleRequested for the mode the owner selected from the page or Home
//     Assistant: the choice is known to be intentional;
//   - settleOther for any other change (physical remote, firmware). On 4 Nov
//     2026 the firmware switched to Smart about 25 s before it reported standby
//     after the power button, so a short window took that for a choice.
const (
	settleRequested = 5
	settleOther     = 60
)

// settle reports, once, a mode kept long enough after this wake was handled.
// Before that the bar's own post-wake Smart (or the gesture being applied) is
// never taken for the owner's choice; a failed wake records nothing, and a
// standby before the window ends discards the change.
func (w *wakeTracker) settle(s wakeSample) (uint32, bool) {
	if !w.handled || w.timer || w.failed || s.identity == "" || s.identity != w.last.identity || s.power != 1 || s.system != 0 || !s.idle || (s.mode != 0 && s.mode != 3) {
		w.settledCount = 0
		return 0, false
	}
	if s.mode != w.settledMode || w.settledCount == 0 {
		if s.mode != w.settledMode {
			w.settledReported = false
		}
		w.settledMode, w.settledCount = s.mode, 0
	}
	w.settledCount++
	need := settleOther
	if w.requested && s.mode == w.requestedMode {
		need = settleRequested
	}
	if w.settledReported || w.settledCount < need {
		return s.mode, false
	}
	w.settledReported = true
	if s.mode == w.requestedMode {
		w.requested = false
	}
	return s.mode, true
}

// smartMemoryPath keeps the last mode the owner chose (smart_mode_memory). It
// survives standby, mains power loss and service restarts, and is rewritten
// only when the remembered mode changes.
var smartMemoryPath = "/var/JBLRemote/smart-mode.json"

func modeName(mode uint32) string {
	if mode == 3 {
		return "Smart"
	}
	return "Standard"
}

// loadSmartMemory returns the remembered mode; Standard when there is none
// yet or the file is not this remote's, as with plain auto_standard.
func loadSmartMemory(path string) string {
	b, e := os.ReadFile(path)
	if e != nil {
		return "Standard"
	}
	switch strings.TrimSpace(string(b)) {
	case `{"mode":"Smart"}`:
		return "Smart"
	}
	return "Standard"
}
func saveSmartMemory(path, mode string) error {
	if mode != "Smart" && mode != "Standard" {
		return errors.New("invalid remembered mode")
	}
	tmp := path + ".new"
	_ = os.Remove(tmp)
	f, e := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	if _, e = f.WriteString(`{"mode":"` + mode + `"}` + "\n"); e == nil {
		e = f.Sync()
	}
	if c := f.Close(); e == nil {
		e = c
	}
	if e != nil {
		os.Remove(tmp)
		return e
	}
	return os.Rename(tmp, path)
}

// Verify all pinned payload members before pausing or restoring a legacy worker.
// Runtime logs and the owned DISABLED marker are deliberately outside its manifest.
func verifyLegacySmart(dir string) (bool, error) {
	info, e := os.Lstat(dir)
	if os.IsNotExist(e) {
		return false, nil
	}
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("Smart Mode helper directory is not recognized")
	}
	read := func(name string) ([]byte, error) {
		p := filepath.Join(dir, name)
		i, e := os.Lstat(p)
		if e != nil || !i.Mode().IsRegular() {
			return nil, errors.New("Smart Mode helper contains a missing or non-regular file")
		}
		return os.ReadFile(p)
	}
	owner, e := read("OWNER.txt")
	if e != nil {
		return false, e
	}
	pin, ok := legacySmartManifests[strings.TrimSpace(string(owner))]
	if !ok {
		return false, errors.New("Unknown Smart Mode helper preserved")
	}
	manifest, e := read("checksums.txt")
	if e != nil {
		return false, e
	}
	if eqDigest(manifest) != pin {
		return false, errors.New("Modified Smart Mode manifest preserved")
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(manifest)), "\n") {
		parts := strings.Split(line, "  ")
		if len(parts) != 2 {
			return false, errors.New("Invalid Smart Mode manifest")
		}
		name := parts[1]
		switch name {
		case "run.sh", "once.sh", "controller.sh", "helper.sh", "hook.expected", "OWNER.txt":
		default:
			return false, errors.New("Unexpected Smart Mode manifest member")
		}
		if seen[name] {
			return false, errors.New("Duplicate Smart Mode manifest member")
		}
		seen[name] = true
		b, e := read(name)
		if e != nil {
			return false, e
		}
		if eqDigest(b) != parts[0] {
			return false, errors.New("Modified Smart Mode payload preserved")
		}
	}
	if len(seen) != 6 {
		return false, errors.New("Incomplete Smart Mode manifest")
	}
	return true, nil
}
func claimLegacySmart(dir string) error {
	exists, e := verifyLegacySmart(dir)
	if e != nil || !exists {
		return e
	}
	p := filepath.Join(dir, "DISABLED")
	if b, e := os.ReadFile(p); e == nil {
		i, err := os.Lstat(p)
		if err == nil && i.Mode().IsRegular() && bytes.Equal(b, []byte(goSmartMarker)) {
			return nil
		}
		return errors.New("Existing Smart Mode disable flag preserved")
	} else if !os.IsNotExist(e) {
		return e
	}
	f, e := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.WriteString(goSmartMarker)
	if e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		_ = os.Remove(p)
	}
	return e
}
func releaseLegacySmart(dir string) (bool, error) {
	p := filepath.Join(dir, "DISABLED")
	i, e := os.Lstat(p)
	if os.IsNotExist(e) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if !i.Mode().IsRegular() {
		return false, nil
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return false, e
	}
	if !bytes.Equal(b, []byte(goSmartMarker)) {
		return false, nil
	}
	exists, e := verifyLegacySmart(dir)
	if e != nil || !exists {
		return false, e
	}
	return true, os.Remove(p)
}
func restoreLegacySmart() error {
	released, e := releaseLegacySmart(legacySmartDir)
	if e != nil || !released {
		return e
	}
	cmd := exec.Command("/bin/sh", legacySmartDir+"/run.sh")
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "LD_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	if e = cmd.Start(); e != nil {
		return e
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
func legacySmartRunning() bool {
	for _, p := range []string{"/tmp/jbl-standard-watch-active", "/tmp/jbl-standard-hook-active", "/tmp/jbl-standard-mutation-active"} {
		if _, e := os.Lstat(p); !os.IsNotExist(e) {
			return true
		}
	}
	return false
}
func (d *device) wakeSample() (wakeSample, error) {
	var s wakeSample
	if e := d.identify(); e != nil {
		return s, e
	}
	var e error
	s.identity = fmt.Sprintf("%d:%s", d.pid, d.start)
	s.power, e = d.read(14191893, 1)
	if e != nil {
		return wakeSample{}, e
	}
	s.system, e = d.read(12962676, 4)
	if e != nil {
		return wakeSample{}, e
	}
	s.mode, e = d.read(13378532, 4)
	if e != nil {
		return wakeSample{}, e
	}
	s.source, e = d.configByte(365)
	if e != nil {
		return wakeSample{}, e
	}
	s.idle = d.idleKeys() && !controlBusy()
	if !d.same() {
		return wakeSample{}, errors.New("Firmware process changed")
	}
	return s, nil
}
func (r *remote) setAutomation(status string, err error) {
	d := r.device
	d.mu.Lock()
	defer d.mu.Unlock()
	d.automation = &automationState{Enabled: true, Engine: "go", Status: status}
	if err != nil {
		d.automation.Error = err.Error()
	}
	log.Printf("Smart Mode automation: %s", status)
	if err != nil {
		log.Printf("Smart Mode automation: %v", err)
	}
}
func (r *remote) monitorAutoStandard(ctx context.Context) {
	owner, e := lockSmartAutomation(goSmartLock)
	if e != nil {
		if r.cfg.AutoStandard {
			r.setAutomation("AUTOMATION_FAILED", e)
		} else {
			log.Printf("Smart Mode restore: %v", e)
		}
		return
	}
	defer owner.Close()
	if !r.cfg.AutoStandard {
		if e := restoreLegacySmart(); e != nil {
			log.Printf("Smart Mode restore: %v", e)
		}
		return
	}
	r.setAutomation("PAUSING_LEGACY", nil)
	if e := claimLegacySmart(legacySmartDir); e != nil {
		r.setAutomation("AUTOMATION_FAILED", e)
		return
	}
	defer func() {
		if e := restoreLegacySmart(); e != nil {
			log.Printf("Smart Mode restore: %v", e)
		}
	}()
	deadline := time.Now().Add(35 * time.Second)
	for legacySmartRunning() {
		if time.Now().After(deadline) {
			r.setAutomation("AUTOMATION_FAILED", errors.New("Legacy Smart Mode worker did not stop; automatic controls paused"))
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
	var tracker wakeTracker
	r.device.mu.Lock()
	manualSeen := r.device.manualModes
	r.device.mu.Unlock()
	remembered := ""
	if r.cfg.SmartModeMemory {
		remembered = loadSmartMemory(smartMemoryPath)
		log.Printf("Smart Mode automation: remembered mode %s", remembered)
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		release, report, held := r.hold("")
		if !held {
			continue
		}
		if ctx.Err() != nil {
			release()
			return
		}
		d := r.device
		d.mu.Lock()
		sample, e := d.wakeSample()
		if e != nil {
			sample = wakeSample{}
		}
		if d.manualModes != manualSeen {
			manualSeen = d.manualModes
			tracker.chosen(d.manualMode)
		}
		status, apply := tracker.observeFor(sample, remembered == "Smart")
		if r.timerWake.Load() && sample.power == 1 && !tracker.handled && !tracker.failed {
			tracker.handled, tracker.timer = true, true
			status, apply = "TIMER_WAKE", false
		}
		if r.cfg.SmartModeMemory {
			if mode, kept := tracker.settle(sample); kept && modeName(mode) != remembered {
				if e := saveSmartMemory(smartMemoryPath, modeName(mode)); e != nil {
					log.Printf("Smart Mode automation: cannot remember %s: %v", modeName(mode), e)
				} else {
					remembered = modeName(mode)
					log.Printf("Smart Mode automation: remembered mode %s", remembered)
				}
			}
		}
		// Keep a failure's reason visible until the wake ends.
		failure := tracker.failureReason
		changed := d.automation == nil || d.automation.Status != status
		d.automation = &automationState{Enabled: true, Engine: "go", Status: status, Error: failure, RememberedMode: remembered}
		if changed {
			log.Printf("Smart Mode automation: %s", status)
		}
		if apply {
			d.automation.Status = "APPLYING_STANDARD"
			d.automationRunning = true
			report("auto_standard")
			// Publish settling status before the bounded native action takes d.mu.
			ready := d.snapshotCurrent()
			r.feed.publish(ready)
		}
		d.mu.Unlock()
		if apply {
			// Finish key release/console restoration even during server shutdown.
			c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			after, err := d.action(c, "standard")
			cancel()
			d.mu.Lock()
			d.automationRunning = false
			if err == nil && after.Mode == "Standard" && d.idleKeys() {
				tracker.handled = true
				d.automation.Status = "STANDARD_THIS_WAKE"
				d.automation.Error = ""
			} else {
				tracker.failed = true
				d.automation.Status = "AUTOMATION_FAILED"
				if err != nil {
					d.automation.Error = err.Error()
				} else {
					d.automation.Error = "Standard Mode was not confirmed"
				}
				tracker.failureReason = d.automation.Error
			}
			log.Printf("Smart Mode automation: %s (%s)", d.automation.Status, d.automation.Error)
			r.feed.publish(d.snapshotCurrent())
			d.mu.Unlock()
		}
		release()
	}
}

func (d *device) awakeForGesture(action string, source *uint32) bool {
	if !d.same() {
		return false
	}
	p, e1 := d.read(14191893, 1)
	s, e2 := d.read(12962676, 4)
	if e1 != nil || e2 != nil || p != 1 || s != 0 {
		return false
	}
	if source == nil {
		return false
	}
	current, e := d.configByte(365)
	if e != nil || current != *source {
		return false
	}
	if action == "standard" || action == "smart" {
		mode, e := d.read(13378532, 4)
		return e == nil && (action == "standard" && mode == 3 || action == "smart" && mode == 0)
	}
	return true
}
func standardSettingsPreserved(before, after state) bool {
	return before.Volume != nil && after.Volume != nil && *before.Volume == *after.Volume && before.Muted != nil && after.Muted != nil && *before.Muted == *after.Muted
}
