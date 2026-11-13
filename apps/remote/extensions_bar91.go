package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"jbl-bar-toolkit/remote/internal/display"
	"jbl-bar-toolkit/remote/internal/extension"
)

// bar91ActionAliases maps the v1 action names to the namespaced extension
// actions. Execute and the reply-timeout policy both resolve through it.
var bar91ActionAliases = map[string]string{
	"eq_apply": "bar91.front-eq.apply", "eq_reset": "bar91.front-eq.reset",
	"center_next":     "bar91.center-level.next",
	"display_message": "bar91.display-message.show", "display_cancel": "bar91.display-message.cancel",
	"display_clock": "bar91.display-clock.set",
	"timer_start":   "bar91.timer.start", "timer_cancel": "bar91.timer.cancel", "timer_extend": "bar91.timer.extend",
}

// Model composition is explicit. A future family backend registers only the
// features it has qualified; it does not reuse 9.1 native actions or EQ tuning.
func (r *remote) registerBar91Extensions() error {
	if err := r.extensions.Register(extension.Module[state]{Descriptor: extension.Descriptor{ID: "bar91.display-feedback", Name: "Front display feedback", Model: "bar91", Included: false, Experimental: true, Reason: display.UnavailableReason}}); err != nil {
		return err
	}
	r.actionAliases = bar91ActionAliases
	if err := r.registerDisplayMessage(); err != nil {
		return err
	}
	if err := r.registerTimers(); err != nil {
		return err
	}
	if err := r.registerDisplayClock(); err != nil {
		return err
	}
	eq := func(action string) extension.Handler[state] {
		return func(ctx context.Context, data json.RawMessage) (state, error) {
			var parameters struct {
				GainsDB []float64 `json:"gains_db"`
			}
			if err := extensionParameters(data, &parameters); err != nil {
				return state{}, err
			}
			return r.device.eqAction(ctx, action, parameters.GainsDB)
		}
	}
	reason := ""
	if eqParserPayload == "" {
		reason = "native helper not bundled"
	}
	if err := r.extensions.Register(extension.Module[state]{
		Descriptor: extension.Descriptor{ID: "bar91.front-eq", Component: "front-eq", Name: "Front EQ", Model: "bar91", Included: eqParserPayload != "", Experimental: true, Reason: reason},
		Actions:    map[string]extension.Handler[state]{"bar91.front-eq.apply": eq("eq_apply"), "bar91.front-eq.reset": eq("eq_reset")},
		Run:        r.monitorFrontEQ,
		Project: func(s state) any {
			if s.EQ == nil {
				return nil
			}
			return s.EQ
		},
	}); err != nil {
		return err
	}
	if err := r.extensions.Register(extension.Module[state]{
		Descriptor: extension.Descriptor{ID: "bar91.bass-offset", Component: "bass-offset", Name: "Bass offset", Model: "bar91", Included: true, Experimental: true},
		Actions: map[string]extension.Handler[state]{"bar91.bass-offset.set": func(ctx context.Context, data json.RawMessage) (state, error) {
			var parameters struct {
				OffsetDB *float64 `json:"offset_db"`
			}
			if err := extensionParameters(data, &parameters); err != nil {
				return state{}, err
			}
			if parameters.OffsetDB == nil || !validBassOffset(*parameters.OffsetDB) {
				return state{}, errors.New("provide offset_db from -3 to +3 dB in 0.5 dB steps")
			}
			if err := ctx.Err(); err != nil {
				return state{}, err
			}
			return r.device.bassOffsetAction(*parameters.OffsetDB)
		}},
		Run: r.monitorBass,
		Project: func(s state) any {
			if s.BassOffset == nil {
				return nil
			}
			return s.BassOffset
		},
	}); err != nil {
		return err
	}
	return r.extensions.Register(extension.Module[state]{
		Descriptor: extension.Descriptor{ID: "bar91.center-level", Component: "center-level", Name: "Center level", Model: "bar91", Included: true, Experimental: true},
		Run:        r.monitorCenter,
		Actions: map[string]extension.Handler[state]{"bar91.center-level.next": func(ctx context.Context, data json.RawMessage) (state, error) {
			var parameters struct{}
			if err := extensionParameters(data, &parameters); err != nil {
				return state{}, err
			}
			s, err := r.device.centerAction(ctx, "center_next")
			if err == nil && s.Sound != nil && s.Sound.Center != nil {
				showCenterLevel(r.display, *s.Sound.Center)
			}
			return s, err
		}},
		Project: func(s state) any {
			if s.Sound == nil || s.Sound.Center == nil {
				return nil
			}
			return map[string]any{"level": s.Sound.Center, "error": s.Sound.CenterError}
		},
	})
}

func extensionParameters(data json.RawMessage, target any) error {
	if len(data) == 0 {
		return nil
	}
	if len(data) > 256 || len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
		return errors.New("extension parameters must be an object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid extension parameters")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid extension parameters")
	}
	return nil
}

// registerDisplayMessage adds custom front-display messages (display_message.go).
// Actions return at once; the manager shows the message in the background.
func (r *remote) registerDisplayMessage() error {
	r.displayPort = barDisplayPort{r}
	r.display = newDisplayManager(r.displayPort, displayLog)
	ready := func() error {
		if r.device == nil {
			return errors.New("native display unavailable")
		}
		return nil
	}
	return r.extensions.Register(extension.Module[state]{
		Descriptor: extension.Descriptor{ID: "bar91.display-message", Component: "messages", Name: "Display messages", Model: "bar91", Included: true, Experimental: true},
		// The messages in course, for the page and Home Assistant.
		Project: func(state) any { return map[string]any{"messages": r.display.List()} },
		Actions: map[string]extension.Handler[state]{
			"bar91.display-message.show": func(ctx context.Context, data json.RawMessage) (state, error) {
				request, err := parseDisplayShow(data)
				if err == nil {
					err = ready()
				}
				if err == nil {
					err = r.display.Show(request)
				}
				return r.backend.snapshot(), err
			},
			"bar91.display-message.cancel": func(ctx context.Context, data json.RawMessage) (state, error) {
				id, err := parseDisplayCancel(data)
				if err == nil {
					r.display.Cancel(id)
				}
				return r.backend.snapshot(), err
			},
		},
		Run: r.display.Run,
	})
}

// registerDisplayClock adds the clock on the front display (display_clock.go),
// below every message, timer and screen of the bar's own.
func (r *remote) registerDisplayClock() error {
	r.clock = newDisplayClock(r.display, r.displayPort, clockPath, displayLog)
	return r.extensions.Register(extension.Module[state]{
		Descriptor: extension.Descriptor{ID: "bar91.display-clock", Component: "display-clock", Name: "Display clock", Model: "bar91", Included: true, Experimental: true},
		Project:    func(state) any { return r.clock.State() },
		Actions: map[string]extension.Handler[state]{
			"bar91.display-clock.set": func(ctx context.Context, data json.RawMessage) (state, error) {
				change, err := parseClockSet(data)
				if err == nil && r.device == nil {
					err = errors.New("native display unavailable")
				}
				if err == nil {
					err = r.clock.Set(change)
				}
				return r.backend.snapshot(), err
			},
		},
		Run: r.clock.Run,
	})
}

// registerTimers adds timers on the front display (timer.go), built on the
// display messages.
func (r *remote) registerTimers() error {
	r.timers = newTimerManager(r.display, r.displayPort, displayLog)
	r.timers.sound = r.playTimerSound
	return r.extensions.Register(extension.Module[state]{
		Descriptor: extension.Descriptor{ID: "bar91.timer", Component: "timer", Name: "Timers", Model: "bar91", Included: true, Experimental: true},
		Actions: map[string]extension.Handler[state]{
			"bar91.timer.start": func(ctx context.Context, data json.RawMessage) (state, error) {
				request, err := parseTimerStart(data)
				if err == nil && r.device == nil {
					err = errors.New("native display unavailable")
				}
				if err == nil {
					_, err = r.timers.Start(request)
				}
				return r.backend.snapshot(), err
			},
			"bar91.timer.extend": func(ctx context.Context, data json.RawMessage) (state, error) {
				id, add, err := parseTimerExtend(data)
				if err == nil {
					err = r.timers.Extend(id, add)
				}
				return r.backend.snapshot(), err
			},
			"bar91.timer.cancel": func(ctx context.Context, data json.RawMessage) (state, error) {
				id, err := parseTimerCancel(data)
				if err == nil {
					err = r.timers.Cancel(id)
				}
				return r.backend.snapshot(), err
			},
		},
		Run:     r.timers.Run,
		Project: func(state) any { return map[string]any{"timers": r.timers.List()} },
	})
}
