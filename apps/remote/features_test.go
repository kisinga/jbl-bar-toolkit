package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPeerNameMatchesConnectedAddress(t *testing.T) {
	config := []byte("[11:22:33:44:55:66]\nName = Other phone\nLinkKey = SECRET\n[AA:BB:CC:DD:EE:FF]\nName = My phone\nLinkKey = PRIVATE\n")
	if peerName(config, "aa:bb:cc:dd:ee:ff") != "My phone" {
		t.Fatal("exact case-insensitive match failed")
	}
	for _, address := range []string{"", "12:34:56:78:90:ab"} {
		if peerName(config, address) != "" {
			t.Fatal("unmatched peer leaked")
		}
	}
	if peerName([]byte("[aa:bb:cc:dd:ee:ff]\nName = bad\x00name"), "aa:bb:cc:dd:ee:ff") != "" {
		t.Fatal("invalid name accepted")
	}
	if peerAddress([]byte("AA:BB:CC:DD:EE:FF\x00tail")) != "aa:bb:cc:dd:ee:ff" {
		t.Fatal("peer address decoding failed")
	}
	if peerAddress([]byte("AA:BB:CC:DD:EE:FF")) != "" {
		t.Fatal("unterminated peer accepted")
	}
}
func TestWiFiParsing(t *testing.T) {
	s := parseWiFi("wpa_state=COMPLETED\nssid=My\\x20Wi-Fi\nip_address=192.168.50.1\npsk=PRIVATE\n", "RSSI=-48\nFREQUENCY=5240\n")
	if !s.Available || !s.Connected || s.SSID != "My Wi-Fi" || s.Signal == nil || *s.Signal != -48 || s.Frequency == nil || *s.Frequency != 5240 {
		t.Fatal("network status not decoded", s)
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), "PRIVATE") {
		t.Fatal("secret returned")
	}
	s = parseWiFi("wpa_state=DISCONNECTED\nssid=Old network\n", "RSSI=999\nFREQUENCY=20\n")
	if s.Connected || s.SSID != "" || s.Signal != nil || s.Frequency != nil {
		t.Fatal("stale or invalid network fields exposed")
	}
	for _, name := range []string{"bad\\xZZ", strings.Repeat("a", 33), "bad\\nname"} {
		if ssidText(name) != "" {
			t.Fatal("invalid SSID accepted")
		}
	}
}
func TestNativeWiFiDatagram(t *testing.T) {
	endpoint := filepath.Join(t.TempDir(), "wpa")
	server, e := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: endpoint, Net: "unixgram"})
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	command := make(chan string, 1)
	go func() {
		b := make([]byte, 256)
		server.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, peer, e := server.ReadFromUnix(b)
		if e == nil {
			command <- string(b[:n])
			server.WriteToUnix([]byte("wpa_state=COMPLETED\nssid=Example\n"), peer)
		}
	}()
	reply, e := wpaRequest(endpoint, "IFNAME=wlan0 STATUS")
	if e != nil || !strings.Contains(reply, "COMPLETED") {
		t.Fatal("datagram exchange failed", e)
	}
	if <-command != "IFNAME=wlan0 STATUS" {
		t.Fatal("unexpected command")
	}
}
func TestHotspotAccessBoundary(t *testing.T) {
	_, lan, _ := net.ParseCIDR("192.168.50.0/24")
	ip := net.ParseIP
	ap := ip("192.168.255.249")
	for _, c := range []struct {
		client, host, local string
		up, want            bool
	}{
		{"192.168.50.2", "192.168.50.1", "192.168.50.1", false, true},
		{"192.168.255.250", "192.168.255.249", "192.168.255.249", true, true},
		{"192.168.255.250", "192.168.255.249", "192.168.50.1", true, false},
		{"192.168.255.250", "192.168.50.1", "192.168.255.249", true, false},
		{"192.168.255.250", "192.168.255.249", "192.168.255.249", false, false},
		{"10.0.0.2", "192.168.255.249", "192.168.255.249", true, false},
		{"192.168.255.250", "attacker.invalid", "192.168.255.249", true, false},
	} {
		var active net.IP
		if c.up {
			active = ap
		}
		if allowedNetwork(ip(c.client), ip(c.host), ip(c.local), lan, active) != c.want {
			t.Fatal("access boundary failed", c)
		}
	}
}
func TestSoundCyclesAndSourceNames(t *testing.T) {
	old, next := uint32(4), uint32(0)
	if !soundObserved("bass_next", &soundState{Bass: &old}, &soundState{Bass: &next}) {
		t.Fatal("bass wraparound rejected")
	}
	if soundObserved("bass_next", &soundState{Bass: &old}, &soundState{Bass: &old}) || soundObserved("bass_next", nil, &soundState{Bass: &next}) {
		t.Fatal("unconfirmed sound change accepted")
	}
	for n, want := range []string{"TV / ARC", "Optical", "Bluetooth", "HDMI", "AUX", "USB", "Chromecast", "AirPlay"} {
		if sourceName(uint32(n)) != want {
			t.Fatal("wrong source", n)
		}
	}
	if sourceName(9) != "Unknown" {
		t.Fatal("unknown source guessed")
	}
}
func TestPushFeedCoalescesSlowClients(t *testing.T) {
	var f stateFeed
	c, closeClient := f.subscribe()
	defer closeClient()
	f.publish(state{Revision: 1})
	f.publish(state{Revision: 2})
	f.publish(state{Revision: 3})
	f.publish(state{Revision: 2})
	if (<-c).Revision != 3 {
		t.Fatal("slow client received stale state")
	}
}
func TestEventStreamInitialAndChangedState(t *testing.T) {
	r, e := newRemote(config{AllowedCIDR: "127.0.0.1/32"})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.stop = ctx.Done()
	server := httptest.NewServer(r)
	defer server.Close()
	client := http.Client{Timeout: 3 * time.Second}
	response, e := client.Get(server.URL + "/api/events")
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	if response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal("not an event stream")
	}
	reader := bufio.NewReader(response.Body)
	read := func() state {
		for {
			line, e := reader.ReadString('\n')
			if e != nil {
				t.Fatal(e)
			}
			if strings.HasPrefix(line, "data: ") {
				var s state
				if e = json.Unmarshal([]byte(strings.TrimSpace(line[6:])), &s); e != nil {
					t.Fatal(e)
				}
				return s
			}
		}
	}
	if read().CSRF != r.csrf {
		t.Fatal("initial state missing CSRF")
	}
	r.feed.publish(state{Revision: 100, Available: true, Input: "AirPlay"})
	s := read()
	if s.Input != "AirPlay" || s.Revision != 100 || s.CSRF != r.csrf {
		t.Fatal("changed state not pushed", s)
	}
	cancel()
}

// The wired port is read from sysfs: a link reports its speed and duplex,
// an unplugged port only that it exists, a missing one nothing.
func TestEthernetParsing(t *testing.T) {
	root := t.TempDir()
	port := func(files map[string]string) {
		dir := filepath.Join(root, "eth0")
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, value := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(value+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if s := parseEthernet(root, "eth0"); s.Available || s.Connected {
		t.Fatalf("missing port: %+v", s)
	}
	port(map[string]string{"carrier": "1", "operstate": "up", "speed": "100", "duplex": "full"})
	if s := parseEthernet(root, "eth0"); !s.Available || !s.Connected || s.SpeedMbps == nil || *s.SpeedMbps != 100 || s.Duplex != "full" {
		t.Fatalf("linked port: %+v", s)
	}
	// Unplugged: carrier 0; the driver may still report an old speed.
	port(map[string]string{"carrier": "0", "operstate": "down", "speed": "100", "duplex": "full"})
	if s := parseEthernet(root, "eth0"); !s.Available || s.Connected || s.SpeedMbps != nil || s.Duplex != "" {
		t.Fatalf("unplugged port: %+v", s)
	}
	// Administratively down: carrier cannot be read.
	port(map[string]string{"operstate": "down"})
	if s := parseEthernet(root, "eth0"); !s.Available || s.Connected {
		t.Fatalf("down port: %+v", s)
	}
	// Link with an unknown speed and duplex.
	port(map[string]string{"carrier": "1", "operstate": "unknown", "speed": "-1", "duplex": "unknown"})
	if s := parseEthernet(root, "eth0"); !s.Connected || s.SpeedMbps != nil || s.Duplex != "" {
		t.Fatalf("unknown link: %+v", s)
	}
}
