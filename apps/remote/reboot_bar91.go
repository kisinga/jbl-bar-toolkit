package main

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"syscall"
	"time"

	"jbl-bar-toolkit/remote/internal/extension"
)

// Restarting the soundbar, for when it stops responding (and for developers):
// the action answers first, then the bar restarts through BusyBox's reboot,
// which goes through init like a power cycle would not. It takes about a
// minute; the page and Home Assistant reconnect by themselves.
const rebootDelay = 1500 * time.Millisecond

var rebootCommand = []string{"/bin/busybox", "reboot"}

// parseReboot accepts only {"confirm":true}: a restart cuts the sound, so a
// request without it (a stray call, an automation typo) is refused.
func parseReboot(data json.RawMessage) error {
	var p struct {
		Confirm bool `json:"confirm"`
	}
	if err := extensionParameters(data, &p); err != nil {
		return err
	}
	if !p.Confirm {
		return errors.New(`restarting the soundbar needs {"confirm":true}`)
	}
	return nil
}

// scheduleReboot runs restart after delay, once the reply has gone out.
func scheduleReboot(delay time.Duration, restart func() error, logf func(string, ...any)) {
	go func() {
		time.Sleep(delay)
		syscall.Sync()
		if err := restart(); err != nil {
			logf("Restart: %v", err)
		}
	}()
}

func (r *remote) registerReboot() error {
	restart := r.rebootRun
	if restart == nil {
		restart = func() error { return exec.Command(rebootCommand[0], rebootCommand[1:]...).Run() }
	}
	return r.extensions.Register(extension.Module[state]{
		Descriptor: extension.Descriptor{ID: "bar91.system", Component: "reboot", Name: "Restart the soundbar", Model: "bar91", Included: true, Experimental: true},
		Actions: map[string]extension.Handler[state]{
			"bar91.system.reboot": func(ctx context.Context, data json.RawMessage) (state, error) {
				if err := parseReboot(data); err != nil {
					return r.backend.snapshot(), err
				}
				if r.device == nil && r.rebootRun == nil {
					return r.backend.snapshot(), errors.New("restart unavailable: the remote does not run on the soundbar")
				}
				displayLog("Restart requested")
				scheduleReboot(rebootDelay, restart, displayLog)
				return r.backend.snapshot(), nil
			},
		},
	})
}
