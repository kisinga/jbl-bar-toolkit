package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"jbl-bar-toolkit/remote/internal/server"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type wifiState struct {
	HotspotIP string `json:"hotspot_ip,omitempty"`
	Available bool   `json:"available"`
	Connected bool   `json:"connected"`
	SSID      string `json:"ssid,omitempty"`
	IP        string `json:"ip,omitempty"`
	Signal    *int   `json:"signal_dbm,omitempty"`
	Frequency *int   `json:"frequency_mhz,omitempty"`
}
type networkCache struct {
	sync.Mutex
	state wifiState
}

var wifiCache networkCache

// ethernetState is the bar's wired port (eth0, ETHERNET on the back panel).
// The firmware drops Wi-Fi while a cable is connected.
type ethernetState struct {
	Available bool   `json:"available"`
	Connected bool   `json:"connected"`
	IP        string `json:"ip,omitempty"`
	SpeedMbps *int   `json:"speed_mbps,omitempty"`
	Duplex    string `json:"duplex,omitempty"`
}

const ethernetInterface = "eth0"

var (
	ethernetSysfs = "/sys/class/net" // tests point it elsewhere
	ethernetCache struct {
		sync.Mutex
		state ethernetState
	}
)

// parseEthernet reads the link of one interface from sysfs: carrier, then
// the negotiated speed and duplex, which the driver only reports with a
// link (unknown otherwise: -1, "unknown" or a read error).
func parseEthernet(root, name string) ethernetState {
	dir := filepath.Join(root, name)
	if st, e := os.Stat(dir); e != nil || !st.IsDir() {
		return ethernetState{}
	}
	s := ethernetState{Available: true}
	read := func(file string) string {
		b, e := os.ReadFile(filepath.Join(dir, file))
		if e != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	if s.Connected = read("carrier") == "1" && read("operstate") != "down"; !s.Connected {
		return s
	}
	if n, e := strconv.Atoi(read("speed")); e == nil && n > 0 && n <= 400000 {
		s.SpeedMbps = &n
	}
	if d := read("duplex"); d == "full" || d == "half" {
		s.Duplex = d
	}
	return s
}

// interfaceIPv4 is the first IPv4 address of a network interface, or "".
func interfaceIPv4(name string) string {
	iface, e := net.InterfaceByName(name)
	if e != nil {
		return ""
	}
	addresses, e := iface.Addrs()
	if e != nil {
		return ""
	}
	for _, address := range addresses {
		if p, ok := address.(*net.IPNet); ok && p.IP.To4() != nil {
			return p.IP.String()
		}
	}
	return ""
}
func readEthernet() ethernetState {
	ethernetCache.Lock()
	defer ethernetCache.Unlock()
	return ethernetCache.state
}
func refreshEthernet() {
	s := parseEthernet(ethernetSysfs, ethernetInterface)
	if s.Connected {
		s.IP = interfaceIPv4(ethernetInterface)
	}
	ethernetCache.Lock()
	ethernetCache.state = s
	ethernetCache.Unlock()
}

func wpaRequest(endpoint, command string) (string, error) {
	nonce := make([]byte, 12)
	if _, e := rand.Read(nonce); e != nil {
		return "", e
	}
	// Abstract client sockets let Android's Wi-Fi UID reply without granting it
	// access to a root-owned temporary directory. connect() pins the peer socket.
	c, e := net.DialUnix("unixgram", &net.UnixAddr{Name: "@jbl-wifi-" + hex.EncodeToString(nonce), Net: "unixgram"}, &net.UnixAddr{Name: endpoint, Net: "unixgram"})
	if e != nil {
		return "", e
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(400 * time.Millisecond))
	if _, e = c.Write([]byte(command)); e != nil {
		return "", e
	}
	b := make([]byte, 16384)
	n, e := c.Read(b)
	if e != nil {
		return "", e
	}
	if n == len(b) {
		return "", errors.New("oversized network response")
	}
	return string(b[:n]), nil
}
func keyValues(s string) map[string]string {
	values := map[string]string{}
	scan := bufio.NewScanner(strings.NewReader(s))
	for scan.Scan() {
		if k, v, ok := strings.Cut(scan.Text(), "="); ok {
			values[k] = v
		}
	}
	return values
}
func ssidText(s string) string {
	// Supplicant STATUS uses printf-style escaping, not a quoted shell argument.
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'x':
				if i+2 >= len(s) {
					return ""
				}
				v, e := hex.DecodeString(s[i+1 : i+3])
				if e != nil {
					return ""
				}
				b = append(b, v[0])
				i += 2
			case '\\', '"':
				b = append(b, s[i])
			case 't':
				b = append(b, '\t')
			default:
				return ""
			}
		} else {
			b = append(b, s[i])
		}
	}
	if len(b) > 32 || !utf8.Valid(b) {
		return ""
	}
	return string(b)
}
func parseWiFi(status, signal string) wifiState {
	v := keyValues(status)
	s := wifiState{Available: v["wpa_state"] != "", Connected: v["wpa_state"] == "COMPLETED"}
	if s.Connected {
		s.SSID = ssidText(v["ssid"])
		if ip := net.ParseIP(v["ip_address"]); ip != nil {
			s.IP = ip.String()
		}
	}
	p := keyValues(signal)
	if n, e := strconv.Atoi(p["RSSI"]); s.Connected && e == nil && n >= -127 && n <= 0 {
		s.Signal = &n
	}
	if n, e := strconv.Atoi(p["FREQUENCY"]); s.Connected && e == nil && n >= 2300 && n <= 7125 {
		s.Frequency = &n
	}
	return s
}
func hotspotAddress() net.IP {
	iface, e := net.InterfaceByName("ap0")
	if e != nil || iface.Flags&net.FlagUp == 0 {
		return nil
	}
	addresses, e := iface.Addrs()
	if e != nil {
		return nil
	}
	for _, address := range addresses {
		if p, ok := address.(*net.IPNet); ok && p.IP.Equal(net.ParseIP("192.168.255.249")) {
			return p.IP
		}
	}
	return nil
}
func readWiFi() wifiState { wifiCache.Lock(); defer wifiCache.Unlock(); return wifiCache.state }
func refreshWiFi() {
	result := wifiState{}
	if ip := hotspotAddress(); ip != nil {
		result.HotspotIP = ip.String()
	}
	for _, endpoint := range []string{"/dev/socket/wpa_wlan0", "/data/wifi/wlan0", "/var/run/wpa_supplicant/wlan0"} {
		if st, e := os.Stat(endpoint); e != nil || st.Mode()&os.ModeSocket == 0 {
			continue
		}
		prefix := "IFNAME=wlan0 "
		status, e := wpaRequest(endpoint, prefix+"STATUS")
		if e != nil || !strings.Contains(status, "wpa_state=") {
			prefix = ""
			status, e = wpaRequest(endpoint, "STATUS")
		}
		if e != nil || !strings.Contains(status, "wpa_state=") {
			continue
		}
		signal, _ := wpaRequest(endpoint, prefix+"SIGNAL_POLL")
		parsed := parseWiFi(status, signal)
		parsed.HotspotIP = result.HotspotIP
		result = parsed
		break
	}
	if result.Connected && result.IP == "" {
		result.IP = interfaceIPv4("wlan0")
	}
	wifiCache.Lock()
	wifiCache.state = result
	wifiCache.Unlock()
}

// monitorNetwork refreshes the Wi-Fi and Ethernet states every 5 s.
func monitorNetwork(ctx context.Context) {
	refreshWiFi()
	refreshEthernet()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshWiFi()
			refreshEthernet()
		}
	}
}

// Retained for existing native fixtures; policy lives in the shared server.
func allowedNetwork(client, host, local net.IP, lan *net.IPNet, ap net.IP) bool {
	var network *net.IPNet
	if ap != nil {
		network = &net.IPNet{IP: ap, Mask: net.CIDRMask(29, 32)}
	}
	return server.AllowedNetwork(client, host, local, lan, network)
}

// The observed setup-hotspot topology is Bar 9.1 policy, not shared server data.
func hotspotNetwork() *net.IPNet {
	address := hotspotAddress()
	if address == nil {
		return nil
	}
	return &net.IPNet{IP: address, Mask: net.CIDRMask(29, 32)}
}
