package main

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// All readings are passive. Sampling shares the device lock and existing SSE
// reader; repeated actions/clients reuse one two-second snapshot.
type systemState struct {
	Sample      uint64        `json:"sample"`
	CPUPercent  *float64      `json:"cpu_percent,omitempty"`
	CPUCores    int           `json:"cpu_cores,omitempty"`
	LoadAverage []float64     `json:"load_average,omitempty"`
	Memory      *memoryState  `json:"memory,omitempty"`
	Storage     *storageState `json:"storage,omitempty"`
	Network     *networkRates `json:"network,omitempty"`
	// EthernetNetwork: the same rates for the wired port (eth0).
	EthernetNetwork *networkRates `json:"ethernet_network,omitempty"`
	TemperatureC    *float64      `json:"temperature_c,omitempty"`
	Processes       *int          `json:"processes,omitempty"`
	UptimeSeconds   *uint64       `json:"uptime_seconds,omitempty"`
}
type memoryState struct {
	Total     uint64 `json:"total_bytes"`
	Available uint64 `json:"available_bytes"`
	Used      uint64 `json:"used_bytes"`
	Estimated bool   `json:"estimated_available"`
}
type storageState struct {
	Total     uint64 `json:"total_bytes"`
	Available uint64 `json:"available_bytes"`
	Used      uint64 `json:"used_bytes"`
}
type networkRates struct {
	Receive  *float64 `json:"receive_bytes_per_second,omitempty"`
	Transmit *float64 `json:"transmit_bytes_per_second,omitempty"`
}
type cpuCounters struct{ total, idle uint64 }
type networkCounters struct{ receive, transmit uint64 }
type systemMonitor struct {
	last     time.Time
	sequence uint64
	cached   *systemState
	cpu      *cpuCounters
	network  *networkCounters
	ethernet *networkCounters
}

func parseCPU(b []byte) (*cpuCounters, int) {
	var counters *cpuCounters
	cores := 0
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if f[0] == "cpu" {
			if len(f) < 5 {
				return nil, 0
			}
			var c cpuCounters
			// guest/guest_nice are already included in user/nice. Count only the
			// first eight fields; waiting for I/O is idle time in this display.
			for i := 1; i < len(f) && i <= 8; i++ {
				n, e := strconv.ParseUint(f[i], 10, 64)
				if e != nil || math.MaxUint64-c.total < n {
					return nil, 0
				}
				c.total += n
				if i == 4 || i == 5 {
					c.idle += n
				}
			}
			counters = &c
		} else if strings.HasPrefix(f[0], "cpu") {
			if _, e := strconv.ParseUint(strings.TrimPrefix(f[0], "cpu"), 10, 32); e == nil {
				cores++
			}
		}
	}
	return counters, cores
}
func cpuUsage(before, after *cpuCounters) *float64 {
	if before == nil || after == nil || after.total <= before.total || after.idle < before.idle {
		return nil
	}
	total, idle := after.total-before.total, after.idle-before.idle
	if idle > total {
		return nil
	}
	p := math.Round(float64(total-idle)/float64(total)*1000) / 10
	return &p
}
func parseMemory(b []byte) *memoryState {
	values := map[string]uint64{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || f[2] != "kB" {
			continue
		}
		n, e := strconv.ParseUint(f[1], 10, 64)
		if e != nil || n > math.MaxUint64/1024 {
			continue
		}
		values[strings.TrimSuffix(f[0], ":")] = n * 1024
	}
	total, ok := values["MemTotal"]
	if !ok || total == 0 {
		return nil
	}
	available, exact := values["MemAvailable"]
	if !exact {
		// Older kernels lack MemAvailable. This cache-aware approximation is
		// labelled as estimated; MemFree alone would exaggerate memory pressure.
		for _, key := range []string{"MemFree", "Buffers", "Cached", "SReclaimable"} {
			n, exists := values[key]
			if !exists && key == "MemFree" {
				return nil
			}
			if math.MaxUint64-available < n {
				return nil
			}
			available += n
		}
		shared := values["Shmem"]
		if shared < available {
			available -= shared
		} else {
			available = 0
		}
	}
	if available > total {
		available = total
	}
	return &memoryState{Total: total, Available: available, Used: total - available, Estimated: !exact}
}
func parseLoads(b []byte) []float64 {
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return nil
	}
	values := make([]float64, 3)
	for i := range values {
		v, e := strconv.ParseFloat(f[i], 64)
		if e != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return nil
		}
		values[i] = v
	}
	return values
}
func parseNetwork(b []byte) *networkCounters { return parseInterfaceCounters(b, "wlan0") }

// parseInterfaceCounters reads one interface's byte counters from /proc/net/dev.
func parseInterfaceCounters(b []byte, iface string) *networkCounters {
	for _, line := range strings.Split(string(b), "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) != iface {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 16 {
			return nil
		}
		rx, e := strconv.ParseUint(f[0], 10, 64)
		if e != nil {
			return nil
		}
		tx, e := strconv.ParseUint(f[8], 10, 64)
		if e != nil {
			return nil
		}
		return &networkCounters{rx, tx}
	}
	return nil
}
func networkUsage(before, after *networkCounters, seconds float64) *networkRates {
	if before == nil || after == nil || seconds <= 0 || after.receive < before.receive || after.transmit < before.transmit {
		return nil
	}
	rx, tx := float64(after.receive-before.receive)/seconds, float64(after.transmit-before.transmit)/seconds
	return &networkRates{Receive: &rx, Transmit: &tx}
}
func storageUsage(s syscall.Statfs_t) *storageState {
	if s.Bsize <= 0 || s.Blocks == 0 || s.Bfree > s.Blocks || s.Bavail > s.Blocks {
		return nil
	}
	size := uint64(s.Bsize)
	if s.Blocks > math.MaxUint64/size {
		return nil
	}
	return &storageState{Total: s.Blocks * size, Available: s.Bavail * size, Used: (s.Blocks - s.Bfree) * size}
}
func systemTemperature() *float64 {
	paths, _ := filepath.Glob("/sys/class/thermal/thermal_zone*/type")
	if len(paths) > 16 {
		paths = paths[:16]
	}
	for _, path := range paths {
		b, e := os.ReadFile(path)
		if e != nil {
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(string(b)))
		if !strings.Contains(kind, "cpu") && !strings.Contains(kind, "soc") {
			continue
		}
		b, e = os.ReadFile(filepath.Join(filepath.Dir(path), "temp"))
		if e != nil {
			continue
		}
		n, e := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			continue
		}
		// Thermal sysfs values are millidegrees Celsius.
		n /= 1000
		if n < -20 || n > 150 {
			continue
		}
		n = math.Round(n*10) / 10
		return &n
	}
	return nil
}
func (m *systemMonitor) sample(now time.Time) *systemState {
	if m.cached != nil && now.Sub(m.last) < 2*time.Second {
		return m.cached
	}
	seconds := now.Sub(m.last).Seconds()
	// A hidden page may leave no subscribers for minutes. Start a new CPU/
	// bandwidth interval instead of presenting that long average as a live one.
	if seconds > 8 || seconds <= 0 {
		m.cpu, m.network, m.ethernet = nil, nil, nil
	}
	m.sequence++
	s := &systemState{Sample: m.sequence}
	if b, e := os.ReadFile("/proc/stat"); e == nil {
		next, cores := parseCPU(b)
		s.CPUCores = cores
		s.CPUPercent = cpuUsage(m.cpu, next)
		m.cpu = next
	} else {
		m.cpu = nil
	}
	if b, e := os.ReadFile("/proc/meminfo"); e == nil {
		s.Memory = parseMemory(b)
	}
	if b, e := os.ReadFile("/proc/loadavg"); e == nil {
		s.LoadAverage = parseLoads(b)
	}
	if b, e := os.ReadFile("/proc/net/dev"); e == nil {
		next := parseNetwork(b)
		s.Network = networkUsage(m.network, next, seconds)
		m.network = next
		wired := parseInterfaceCounters(b, ethernetInterface)
		s.EthernetNetwork = networkUsage(m.ethernet, wired, seconds)
		m.ethernet = wired
	} else {
		m.network, m.ethernet = nil, nil
	}
	if b, e := os.ReadFile("/proc/uptime"); e == nil {
		f := strings.Fields(string(b))
		if len(f) > 0 {
			v, e := strconv.ParseFloat(f[0], 64)
			if e == nil && !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v < float64(math.MaxUint64) {
				n := uint64(v)
				s.UptimeSeconds = &n
			}
		}
	}
	if entries, e := os.ReadDir("/proc"); e == nil {
		n := 0
		for _, entry := range entries {
			if _, e := strconv.ParseUint(entry.Name(), 10, 32); e == nil {
				n++
			}
		}
		s.Processes = &n
	}
	var fs syscall.Statfs_t
	if syscall.Statfs("/var", &fs) == nil {
		s.Storage = storageUsage(fs)
	}
	s.TemperatureC = systemTemperature()
	m.cached, m.last = s, now
	return s
}
