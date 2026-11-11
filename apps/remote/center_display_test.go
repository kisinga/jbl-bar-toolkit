package main

import (
	"testing"
	"time"
)

func TestCenterLevelShowsOnTheDisplay(t *testing.T) {
	f, m, stop := startDisplay(t, vfdLevel3None)
	defer stop()
	if centerDisplayText(0) != "CTR 1" || centerDisplayText(5) != "CTR 6" {
		t.Fatalf("%q %q", centerDisplayText(0), centerDisplayText(5))
	}
	if _, err := displayText(centerDisplayText(3)); err != nil {
		t.Fatalf("not a display text: %v", err)
	}
	// Steps in a row replace each other: the last level shows.
	showCenterLevel(m, 2)
	showCenterLevel(m, 3)
	waitScreen(t, f, "text:CTR 4", 10*time.Second)
	showCenterLevel(nil, 1) // no display messages: nothing, no panic
}
