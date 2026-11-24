package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"jbl-bar-toolkit/remote/internal/server"
)

// The composition adapter keeps native control and optional feature policy out
// of the shared server. Other backends implement server.Backend directly.
type bar91Backend struct{ remote *remote }

func (b bar91Backend) Snapshot() state { return b.remote.snapshot() }
func (b bar91Backend) Features() server.Features {
	actions := []string{
		"volume_up", "volume_down", "mute", "power", "standard", "smart", "night_toggle",
		"av_sync_set", "calibrate", "sub_pair",
		"input_tv", "input_hdmi", "input_bluetooth", "bluetooth_pair", "bass_next", "atmos_next", "rear_next",
		"media_play", "media_pause", "media_stop", "media_previous", "media_next",
	}
	aliases := []string{}
	for alias, target := range b.remote.actionAliases {
		if _, ok := b.remote.extensions.Lookup(target); ok {
			aliases = append(aliases, alias)
		}
	}
	sort.Strings(aliases)
	actions = append(actions, aliases...)
	return server.Features{Schema: 1, Model: "bar91", Name: "JBL Bar 9.1", Actions: actions, Extensions: b.remote.extensions.List(), Manuals: bar91Manuals}
}

// hold takes the shared action gate for a native background writer. action
// names a control clients should wait for; empty keeps a short monitor out of
// the reported control while state reads still use the cached snapshot.
// Monitors tick in phase, so a busy gate is retried briefly (retryGate).
func (r *remote) hold(action string) (release func(), report func(string), ok bool) {
	if r.http == nil {
		if !retryGate(r.actionMu.TryLock) {
			return nil, nil, false
		}
		return r.actionMu.Unlock, func(string) {}, true
	}
	var h *server.Hold[state]
	if !retryGate(func() bool { h, ok = r.http.TryHold(action); return ok }) {
		return nil, nil, false
	}
	return h.Release, h.Report, true
}

func (b bar91Backend) Execute(ctx context.Context, request server.Command) (state, error) {
	return b.remote.execute(ctx, request)
}

// eqActionTimeout bounds an EQ tuning change. The mute restore that follows
// has its own budget (eqMuteBudget) and must fit in the extra 10 s the server
// keeps the reply open, so a slow or expired change still reports its result.
const eqActionTimeout = 20 * time.Second

// bar91ActionTimeout is the Bar 9.1 reply-timeout policy. The shared server
// only applies it; native action names stay in this composition. Legacy v1
// names resolve through the same aliases as Execute.
func bar91ActionTimeout(request server.Command) time.Duration {
	action := request.Action
	if target, ok := bar91ActionAliases[action]; ok {
		action = target
	}
	switch action {
	case "av_sync_set":
		return avSyncTimeout
	case "calibrate", "sub_pair":
		return setupTimeout
	case "bar91.front-eq.apply", "bar91.front-eq.reset", "bar91.front-eq.cancel":
		return eqActionTimeout
	}
	return 15 * time.Second
}

func (r *remote) execute(ctx context.Context, request server.Command) (state, error) {
	if r.device != nil && r.device.calibrating() {
		return r.snapshot(), errors.New("calibration is running; wait for it to finish")
	}
	if request.Action == "calibrate" || request.Action == "sub_pair" || request.Action == "av_sync_set" {
		if r.device == nil {
			return r.snapshot(), errors.New("native setup controls unavailable")
		}
		if len(request.Parameters) != 0 || request.GainsDB != nil {
			return r.snapshot(), errors.New("this control does not accept extension or EQ parameters")
		}
		var s state
		var err error
		switch request.Action {
		case "av_sync_set":
			if request.Value == nil {
				return r.snapshot(), errors.New("provide the audio sync value in ms")
			}
			s, err = r.device.avSyncAction(ctx, *request.Value)
		default:
			if request.Value != nil {
				return r.snapshot(), errors.New("this control takes no value")
			}
			if request.Action == "calibrate" {
				s, err = r.device.calibrateAction(ctx)
			} else {
				s, err = r.device.subPairAction(ctx)
			}
		}
		return r.decorate(s), err
	}
	if request.Value != nil {
		return r.snapshot(), errors.New("a value requires a value control")
	}
	var s state
	var e error
	action := request.Action
	if alias, ok := r.actionAliases[action]; ok {
		action = alias
	}
	parameters := request.Parameters
	if request.GainsDB != nil {
		if len(parameters) != 0 {
			e = errors.New("choose extension parameters or legacy band values")
		} else {
			parameters, _ = json.Marshal(map[string]any{"gains_db": request.GainsDB})
		}
	}
	if e == nil {
		if handler, ok := r.extensions.Lookup(action); ok {
			s, e = handler(ctx, parameters)
		} else if len(parameters) != 0 {
			e = errors.New("parameters require an included extension")
		} else {
			s, e = r.backend.action(ctx, request.Action)
		}
	}
	return r.decorate(s), e
}
