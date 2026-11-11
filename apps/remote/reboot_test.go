package main

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

func TestRebootNeedsConfirm(t *testing.T) {
	if err := parseReboot(json.RawMessage(`{"confirm":true}`)); err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{``, `{}`, `{"confirm":false}`, `{"confirm":true,"now":1}`} {
		if parseReboot(json.RawMessage(in)) == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestRebootRunsAfterTheReply(t *testing.T) {
	var synced, ran atomic.Int32
	saved := syncDisks
	syncDisks = func() { synced.Add(1) }
	t.Cleanup(func() { syncDisks = saved })
	scheduleReboot(50*time.Millisecond, func() error {
		if synced.Load() != 1 {
			t.Error("restarted before the disks were synced")
		}
		ran.Add(1)
		return nil
	}, t.Logf)
	if ran.Load() != 0 {
		t.Fatal("restarted before the delay")
	}
	// Generous deadline: under -race on a busy CI runner the goroutine can be late.
	for deadline := time.Now().Add(5 * time.Second); ran.Load() == 0 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if ran.Load() != 1 {
		t.Fatal("not restarted")
	}
}
