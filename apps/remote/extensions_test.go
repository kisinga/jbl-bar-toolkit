package main

import (
	"context"
	"encoding/json"
	"jbl-bar-toolkit/remote/internal/extension"
	"jbl-bar-toolkit/remote/internal/server"
	"strings"
	"testing"
	"time"
)

type fixtureBackend struct{ calls int }

func (b *fixtureBackend) snapshot() state { return state{Available: true, Power: "On"} }
func (b *fixtureBackend) action(context.Context, string) (state, error) {
	b.calls++
	return b.snapshot(), nil
}

func TestExtensionsShareHTTPGuardsAndSerialization(t *testing.T) {
	r := testRemote(t)
	backend := &fixtureBackend{}
	r.backend = backend
	calls := 0
	if err := r.extensions.Register(extension.Module[state]{
		Descriptor: extension.Descriptor{ID: "test.feature", Included: true},
		Actions: map[string]extension.Handler[state]{"test.feature.run": func(ctx context.Context, data json.RawMessage) (state, error) {
			calls++
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("action has no deadline")
			}
			return backend.snapshot(), nil
		}}, Project: func(s state) any { return s.Power },
	}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		ip     string
		csrf   bool
		origin string
	}{
		{"10.0.0.2", true, ""}, {"192.168.50.2", false, ""}, {"192.168.50.2", true, "http://attacker.invalid"},
	} {
		if w := request(r, "POST", "/api/action", `{"action":"test.feature.run"}`, c.ip, c.csrf, c.origin); w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
	r.actionMu.Lock()
	w := request(r, "POST", "/api/action", `{"action":"test.feature.run"}`, "192.168.50.2", true, "")
	r.actionMu.Unlock()
	if w.Code != 409 || calls != 0 {
		t.Fatal("busy or rejected extension executed")
	}
	w = request(r, "POST", "/api/action", `{"action":"test.feature.run","parameters":{}}`, "192.168.50.2", true, "")
	if w.Code != 200 || calls != 1 || backend.calls != 0 || !strings.Contains(w.Body.String(), `"test.feature":"On"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(r, "GET", "/api/state", "", "192.168.50.2", false, "")
	if !strings.Contains(w.Body.String(), `"test.feature":"On"`) {
		t.Fatal("backend snapshot lacks feature state")
	}
	if w = request(r, "GET", "/api/features", "", "10.0.0.2", false, ""); w.Code != 403 {
		t.Fatal("feature discovery leaked")
	}
	if w = request(r, "GET", "/api/features", "", "192.168.50.2", false, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"test.feature.run"`) {
		t.Fatal("feature discovery missing")
	}
}

func TestExtensionParametersAreTyped(t *testing.T) {
	for _, data := range []string{`null`, `[]`, `{"command":"id"}`, `{} {}`, `{"gains_db":["wrong"]}`} {
		var target struct {
			GainsDB []float64 `json:"gains_db"`
		}
		if extensionParameters(json.RawMessage(data), &target) == nil {
			t.Fatal("accepted", data)
		}
	}
	var target struct {
		GainsDB []float64 `json:"gains_db"`
	}
	if extensionParameters(json.RawMessage(`{"gains_db":[1,2]}`), &target) != nil || len(target.GainsDB) != 2 {
		t.Fatal("valid typed parameters rejected")
	}
	r := testRemote(t)
	w := request(r, "POST", "/api/action", `{"action":"mute","parameters":{}}`, "192.168.50.2", true, "")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "included extension") {
		t.Fatal("core action accepted extension parameters")
	}
}

func TestBar91ReplyTimeoutCoversEQNamesAndAliases(t *testing.T) {
	for _, action := range []string{"eq_apply", "eq_reset", "eq_cancel", "bar91.front-eq.apply", "bar91.front-eq.reset", "bar91.front-eq.cancel"} {
		timeout := bar91ActionTimeout(server.Command{Action: action})
		if timeout != eqActionTimeout {
			t.Fatalf("%s: %v", action, timeout)
		}
		// The server keeps the reply open for the timeout plus 10 s; the mute
		// restore after an expired change must fit in that margin.
		if timeout <= 15*time.Second || eqMuteBudget >= 10*time.Second {
			t.Fatalf("%s: restore budget %v does not fit after %v", action, eqMuteBudget, timeout)
		}
	}
	for alias, target := range bar91ActionAliases {
		if bar91ActionTimeout(server.Command{Action: alias}) != bar91ActionTimeout(server.Command{Action: target}) {
			t.Fatalf("alias %s and %s have different timeouts", alias, target)
		}
	}
	if bar91ActionTimeout(server.Command{Action: "center_next"}) != 15*time.Second || bar91ActionTimeout(server.Command{Action: "mute"}) != 15*time.Second {
		t.Fatal("ordinary controls keep the default timeout")
	}
}

func TestEQMuteRestoreHoldsTheActionGate(t *testing.T) {
	// The mute restore runs before the action returns, with its own budget, so
	// no other control runs until it has finished.
	r := testRemote(t)
	r.backend = &fixtureBackend{}
	restoring, release := make(chan struct{}), make(chan struct{})
	muted := true
	if err := r.extensions.Register(extension.Module[state]{
		Descriptor: extension.Descriptor{ID: "test.eq", Included: true},
		Actions: map[string]extension.Handler[state]{"test.eq.apply": func(context.Context, json.RawMessage) (state, error) {
			budget, stop := context.WithTimeout(context.Background(), eqMuteBudget)
			defer stop()
			first := true
			e := muteRestore{
				set:  func(c context.Context, want bool) error { muted = want; return c.Err() },
				read: func() (bool, error) { return muted, nil },
				sleep: func(time.Duration) {
					if first {
						first = false
						close(restoring)
						<-release
					}
				},
			}.run(budget, false)
			return state{Available: true, Power: "On"}, e
		}},
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan int)
	go func() {
		done <- request(r, "POST", "/api/action", `{"action":"test.eq.apply"}`, "192.168.50.2", true, "").Code
	}()
	<-restoring
	if w := request(r, "POST", "/api/action", `{"action":"mute"}`, "192.168.50.2", true, ""); w.Code != 409 || !strings.Contains(w.Body.String(), "Another control") {
		t.Fatal("a control ran during the mute restore", w.Code, w.Body.String())
	}
	close(release)
	if code := <-done; code != 200 || muted {
		t.Fatal("restore did not finish", code, muted)
	}
}

func TestDisplayFeedbackRemainsUnavailableWithoutNativeEntry(t *testing.T) {
	r := testRemote(t)
	w := request(r, "GET", "/api/features", "", "192.168.50.2", false, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var result struct {
		Extensions []extension.Descriptor `json:"extensions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	for _, feature := range result.Extensions {
		if feature.ID == "bar91.display-feedback" {
			if feature.Included || len(feature.Actions) != 0 || !strings.Contains(feature.Reason, "not qualified") {
				t.Fatal("unqualified native display enabled", feature)
			}
			return
		}
	}
	t.Fatal("missing display qualification status")
}
