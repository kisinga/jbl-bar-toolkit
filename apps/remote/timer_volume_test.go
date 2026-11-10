package main

import (
	"context"
	"math"
	"sync"
	"testing"
)

// fakeBarVolume answers like the bar: a Cast level sets the native volume one
// step above level × 32, so a correction is needed.
type fakeBarVolume struct {
	mu     sync.Mutex
	volume uint32
	muted  bool
	calls  int
	onCall func(int)
}

func (f *fakeBarVolume) ops() volumeOps {
	return volumeOps{
		snapshot: func() state {
			f.mu.Lock()
			defer f.mu.Unlock()
			v, m := f.volume, f.muted
			return state{Volume: &v, Muted: &m}
		},
		control: func(ctx context.Context, r castControlRequest) (castControlResult, error) {
			if err := ctx.Err(); err != nil {
				return castControlResult{}, err
			}
			f.mu.Lock()
			f.calls++
			n := f.calls
			if r.Muted != nil {
				f.muted = *r.Muted
			}
			if r.Level != nil {
				f.volume = uint32(math.Round(*r.Level*castVolumeSteps)) + 1
			}
			f.mu.Unlock()
			if f.onCall != nil {
				f.onCall(n)
			}
			return castControlResult{}, nil
		},
	}
}

// Stopped during the correction, the volume has moved: it is put back all
// the same, and so is the mute (review of #45).
func TestTimerVolumeRestoredAfterAPartialChange(t *testing.T) {
	bar := &fakeBarVolume{volume: 4, muted: true}
	ctx, stop := context.WithCancel(context.Background())
	bar.onCall = func(n int) {
		if n == 2 { // after unmute and the first volume step
			stop()
		}
	}
	change := bar.ops().raise(ctx, 10)
	if bar.volume == 4 || bar.muted {
		t.Fatalf("nothing changed: %+v", bar)
	}
	if change.volume == nil || *change.volume != 4 || !change.unmuted {
		t.Fatalf("change not recorded: %+v", change)
	}
	bar.onCall = nil
	bar.ops().restore(context.Background(), change)
	if bar.volume != 4 || !bar.muted {
		t.Fatalf("not put back: volume %d muted %v", bar.volume, bar.muted)
	}
}

func TestTimerVolumeRaisedWithCorrectionNeverLowered(t *testing.T) {
	bar := &fakeBarVolume{volume: 7}
	change := bar.ops().raise(context.Background(), 10)
	if bar.volume != 10 || change.volume == nil || *change.volume != 7 || change.unmuted {
		t.Fatalf("volume %d, change %+v", bar.volume, change)
	}
	loud := &fakeBarVolume{volume: 20}
	if change := loud.ops().raise(context.Background(), 10); change.volume != nil || loud.calls != 0 {
		t.Fatalf("lowered: %+v %d calls", change, loud.calls)
	}
}
