package main

import (
	"math"
	"syscall"
	"testing"
	"time"
)

func TestCPUUsesIntervalsAndDoesNotDoubleCountGuests(t *testing.T) {
	a, cores := parseCPU([]byte("cpu 100 0 50 800 40 5 5 0 90 0\ncpu0 1 2 3 4\ncpu1 1 2 3 4\n"))
	if a == nil || a.total != 1000 || a.idle != 840 || cores != 2 {
		t.Fatalf("%+v cores%d", a, cores)
	}
	b, _ := parseCPU([]byte("cpu 120 0 60 850 60 5 5 0 110 0\n"))
	got := cpuUsage(a, b)
	if got == nil || *got != 30 {
		t.Fatalf("usage%v", got)
	}
	if cpuUsage(nil, b) != nil || cpuUsage(b, b) != nil || cpuUsage(b, a) != nil || cpuUsage(&cpuCounters{100, 0}, &cpuCounters{101, 2}) != nil {
		t.Fatal("warmup/reset invalid interval accepted")
	}
	if p, _ := parseCPU([]byte("cpu 1 nope 3 4")); p != nil {
		t.Fatal("corrupt counters accepted")
	}
	if p, _ := parseCPU([]byte("cpu 18446744073709551615 1 0 0")); p != nil {
		t.Fatal("overflow accepted")
	}
}
func TestMemoryAccountsForReclaimableCache(t *testing.T) {
	exact := parseMemory([]byte("MemTotal: 1000 kB\nMemAvailable: 700 kB\nMemFree: 100 kB\n"))
	if exact == nil || exact.Estimated || exact.Total != 1024000 || exact.Used != 307200 {
		t.Fatalf("%+v", exact)
	}
	old := parseMemory([]byte("MemTotal: 1000 kB\nMemFree: 100 kB\nBuffers: 50 kB\nCached: 300 kB\nSReclaimable: 20 kB\nShmem: 10 kB\n"))
	if old == nil || !old.Estimated || old.Available != 460*1024 || old.Used != 540*1024 {
		t.Fatalf("%+v", old)
	}
	if parseMemory([]byte("MemTotal: 100 kB\n")) != nil || parseMemory([]byte("MemTotal: 18446744073709551615 kB\nMemFree: 10 kB\n")) != nil {
		t.Fatal("missing/overflow memory accepted")
	}
	clamp := parseMemory([]byte("MemTotal: 100 kB\nMemAvailable: 200 kB\n"))
	if clamp == nil || clamp.Used != 0 {
		t.Fatal("memory underflow")
	}
}
func TestNetworkRatesRejectResetsAndUseOnlyWiFi(t *testing.T) {
	sample := []byte("lo: 999 0 0 0 0 0 0 0 999 0 0 0 0 0 0 0\nwlan0: 1000 1 0 0 0 0 0 0 3000 1 0 0 0 0 0 0\n")
	n := parseNetwork(sample)
	if n == nil || n.receive != 1000 || n.transmit != 3000 {
		t.Fatalf("%+v", n)
	}
	rates := networkUsage(n, &networkCounters{3000, 4000}, 2)
	if rates == nil || *rates.Receive != 1000 || *rates.Transmit != 500 {
		t.Fatalf("%+v", rates)
	}
	if networkUsage(nil, n, 2) != nil || networkUsage(n, &networkCounters{0, 0}, 2) != nil || networkUsage(n, n, 0) != nil || parseNetwork([]byte("wlan0: bad")) != nil {
		t.Fatal("missing/reset network accepted")
	}
	// The wired port is counted on its own, never mixed with Wi-Fi.
	wired := append(sample, []byte("  eth0: 5000 9 0 0 0 0 0 0 7000 9 0 0 0 0 0 0\n")...)
	if e := parseInterfaceCounters(wired, "eth0"); e == nil || e.receive != 5000 || e.transmit != 7000 {
		t.Fatalf("eth0 %+v", e)
	}
	if w := parseNetwork(wired); w == nil || w.receive != 1000 || w.transmit != 3000 {
		t.Fatalf("wlan0 with eth0 %+v", w)
	}
	if parseInterfaceCounters(sample, "eth0") != nil {
		t.Fatal("eth0 counters without eth0")
	}
}
func TestStorageDistinguishesAvailableAndFreeBlocks(t *testing.T) {
	fs := syscall.Statfs_t{Bsize: 4096, Blocks: 100, Bfree: 40, Bavail: 35}
	got := storageUsage(fs)
	if got == nil || got.Total != 409600 || got.Used != 245760 || got.Available != 143360 {
		t.Fatalf("%+v", got)
	}
	fs.Bfree = 101
	if storageUsage(fs) != nil {
		t.Fatal("invalid free blocks accepted")
	}
	fs = syscall.Statfs_t{Bsize: 4096, Blocks: math.MaxUint64}
	if storageUsage(fs) != nil {
		t.Fatal("overflow storage accepted")
	}
}
func TestLoadParsingAndSampleCache(t *testing.T) {
	if got := parseLoads([]byte("0.50 1.25 2.75 1/100 12")); len(got) != 3 || got[1] != 1.25 {
		t.Fatalf("%v", got)
	}
	for _, bad := range []string{"NaN 1 2", "1 +Inf 2", "-1 0 0", "1 2"} {
		if parseLoads([]byte(bad)) != nil {
			t.Fatal("invalid load", bad)
		}
	}
	now := time.Now()
	m := systemMonitor{last: now, sequence: 42, cached: &systemState{Sample: 42}}
	if m.sample(now.Add(time.Second)).Sample != 42 || m.sequence != 42 {
		t.Fatal("client/action sampled twice within interval")
	}
}
