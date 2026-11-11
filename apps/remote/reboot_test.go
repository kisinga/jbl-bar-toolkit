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
	var ran atomic.Int32
	scheduleReboot(50*time.Millisecond, func() error { ran.Add(1); return nil }, t.Logf)
	if ran.Load() != 0 {
		t.Fatal("restarted before the delay")
	}
	time.Sleep(200 * time.Millisecond)
	if ran.Load() != 1 {
		t.Fatal("not restarted")
	}
}
