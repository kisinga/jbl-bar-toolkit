package usbsetup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"howett.net/plist"
)

const testConfig = "format=1\naction=check\nssh=false\nweb_remote=true\nauto_standard=false\nallowed_cidr=192.168.9.0/24\nweb_port=80\n"

func fixtureFiles() map[string][]byte {
	files := map[string][]byte{entry: []byte("CLI(CLI_exec sh /mnt/sda1/JBLToolkit/dispatch.sh &)\r\n"), configPath: []byte(testConfig), "README.txt": []byte("fixture"), "LICENSE.txt": []byte("fixture"), "SETUP.html": []byte("fixture"), "JBLToolkit/package.json": []byte(`{"schema":1,"version":"0.3.0-dev.10","model":"bar91","profile":"bar91-23.06.41.80","architecture":"linux-armv7","selection":"automatic","dispatcher_schema":1}`)}
	for _, name := range []string{"common.sh", "dispatch.sh", "launch.sh", "recover.sh", "check.sh", "verify.sh", "parse-config.awk", "ssh-update.sh"} {
		files["JBLToolkit/"+name] = []byte("# fixture " + name)
	}
	elf := make([]byte, 32)
	copy(elf, []byte{127, 'E', 'L', 'F', 1, 1, 1})
	elf[18] = 40
	files["JBLToolkit/files/remote"] = elf
	var bootstrap strings.Builder
	for _, n := range []string{"common.sh", "dispatch.sh", "launch.sh", "recover.sh"} {
		fmt.Fprintf(&bootstrap, "%x  %s\n", sha256.Sum256(files["JBLToolkit/"+n]), n)
	}
	files["JBLToolkit/BOOTSTRAP.SHA256SUMS"] = []byte(bootstrap.String())
	names := []string{}
	for n := range files {
		if strings.HasPrefix(n, "JBLToolkit/") && n != configPath {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var manifest strings.Builder
	for _, n := range names {
		fmt.Fprintf(&manifest, "%x  %s\n", sha256.Sum256(files[n]), strings.TrimPrefix(n, "JBLToolkit/"))
	}
	files["JBLToolkit/SHA256SUMS"] = []byte(manifest.String())
	return files
}
func fixtureZip(t *testing.T, files map[string][]byte) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "usb.zip")
	f, e := os.Create(name)
	if e != nil {
		t.Fatal(e)
	}
	z := zip.NewWriter(f)
	for name, data := range files {
		w, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(data); e != nil {
			t.Fatal(e)
		}
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	return name
}
func TestPackageAndStaging(t *testing.T) {
	p, e := LoadPackage(fixtureZip(t, fixtureFiles()))
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	root, e := os.OpenRoot(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if e = p.Apply(root, testConfig); e != nil {
		t.Fatal(e)
	}
	next := strings.Replace(testConfig, "action=check", "action=update", 1)
	if e = p.Apply(root, next); e != nil {
		t.Fatal(e)
	}
	b, _ := root.ReadFile(configPath)
	if string(b) != next {
		t.Fatal("configuration not updated")
	}
	if e = root.WriteFile("JBLToolkit/dispatch.sh", []byte("modified"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = p.Apply(root, testConfig); e == nil {
		t.Fatal("modified toolkit merged")
	}
	b, _ = root.ReadFile(configPath)
	if string(b) != next {
		t.Fatal("failed validation changed config")
	}
}
func TestRefusals(t *testing.T) {
	for _, kind := range []string{"traversal", "corrupt", "recovery", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			f := fixtureFiles()
			switch kind {
			case "traversal":
				f["../secret"] = []byte("bad")
			case "corrupt":
				f["JBLToolkit/dispatch.sh"] = []byte("bad")
			case "recovery":
				f[entry] = []byte("other entry")
			case "duplicate":
				f["jbltoolkit/config.txt"] = f[configPath]
			}
			if _, e := LoadPackage(fixtureZip(t, f)); e == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
	for _, config := range []string{testConfig + "ssh=true\n", testConfig + "wifi_ssid_hex=00\n", strings.Replace(testConfig, "192.168.9.0/24", "8.8.8.0/24", 1), testConfig + "bad=$(id)\n", strings.Replace(testConfig, "action=check", "action=check|install", 1), testConfig + "smart_mode_memory=true\n"} {
		if _, e := ParseConfig(config); e == nil {
			t.Fatal("unsafe config accepted")
		}
	}
	p, e := LoadPackage(fixtureZip(t, fixtureFiles()))
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "photos.txt"), []byte("keep"), 0600)
	root, _ := os.OpenRoot(dir)
	defer root.Close()
	if e = p.Apply(root, testConfig); e == nil {
		t.Fatal("nonempty USB modified")
	}
	if _, e = os.Stat(filepath.Join(dir, entry)); !os.IsNotExist(e) {
		t.Fatal("entry activated on failure")
	}
}
func testServer(t *testing.T) (*Server, Drive) {
	t.Helper()
	s, e := NewServer("0.3.0-dev.10", "unused", fixtureZip(t, fixtureFiles()))
	if e != nil {
		t.Fatal(e)
	}
	s.Eject = func(context.Context, Drive) error { return nil }
	s.Origin = "http://127.0.0.1:45678"
	d := Drive{ID: "fixture", Fingerprint: "physical", Device: "/dev/fixture", Name: "Fixture USB", Size: 128 << 20, Mount: t.TempDir(), Eligible: true}
	s.List = func(context.Context) ([]Drive, error) { return []Drive{d}, nil }
	return s, d
}
func call(s *Server, method, path string, body any, token bool) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, s.Origin+path, bytes.NewReader(b))
	if token {
		r.Header.Set("X-Session-Token", s.Token)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", s.Origin)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func TestHTTPGuardsAndPrepare(t *testing.T) {
	s, d := testServer(t)
	if w := call(s, "POST", "/api/prepare", nil, false); w.Code != 403 {
		t.Fatal("missing session accepted")
	}
	if w := call(s, "GET", "/favicon.svg", nil, false); w.Code != 200 || w.Header().Get("Content-Type") != "image/svg+xml" {
		t.Fatal("setup favicon", w.Code, w.Header())
	}
	r := httptest.NewRequest("GET", "http://attacker.invalid/", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("DNS rebinding host accepted")
	}
	req := map[string]any{"ID": d.ID, "Config": testConfig, "Confirm": d.Device}
	if w = call(s, "POST", "/api/prepare", req, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	req["ID"] = "removed"
	if w = call(s, "POST", "/api/prepare", req, true); w.Code != 400 {
		t.Fatal("unknown drive accepted")
	}
	req["ID"] = d.ID
	req["Format"] = true
	called := false
	s.Format = func(context.Context, Drive, string, string) error { called = true; return nil }
	if w = call(s, "POST", "/api/prepare", req, true); w.Code != 400 || called {
		t.Fatal("format without exact confirmation")
	}
	req["Confirm"] = "ERASE " + d.Device
	if w = call(s, "POST", "/api/prepare", req, true); w.Code != 200 || !called {
		t.Fatal("confirmed format was not dispatched")
	}
	s.RestartRequired = true
	if w = call(s, "POST", "/api/prepare", req, true); w.Code != 400 {
		t.Fatal("old running setup prepared after self-update")
	}
}
func TestReconnectAndSavedSSH(t *testing.T) {
	s, d := testServer(t)
	calls := 0
	s.List = func(context.Context) ([]Drive, error) {
		calls++
		next := d
		if calls > 1 {
			next.Fingerprint = "replacement"
		}
		return []Drive{next}, nil
	}
	w := call(s, "POST", "/api/prepare", map[string]any{"ID": d.ID, "Config": testConfig, "Confirm": d.Device}, true)
	if w.Code != 400 {
		t.Fatal("device replacement accepted")
	}
	if _, e := os.Stat(filepath.Join(d.Mount, entry)); !os.IsNotExist(e) {
		t.Fatal("replacement target was written")
	}
	s.List = func(context.Context) ([]Drive, error) { return []Drive{d}, nil }
	p, _ := LoadPackage(s.PackagePath)
	root, _ := os.OpenRoot(d.Mount)
	saved := strings.Replace(testConfig, "ssh=false", "ssh=true", 1) + "ssh_password_hash=$6$salt$" + strings.Repeat("a", 86) + "\n"
	if e := p.Apply(root, saved); e != nil {
		t.Fatal(e)
	}
	root.Close()
	fresh := strings.Replace(testConfig, "ssh=false", "ssh=true", 1)
	w = call(s, "POST", "/api/prepare", map[string]any{"ID": d.ID, "Config": fresh, "Confirm": d.Device, "KeepSSH": true}, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = call(s, "GET", "/api/drives", nil, true)
	if strings.Contains(w.Body.String(), "$6$") {
		t.Fatal("drive listing leaked saved credentials")
	}
}
func TestDriveListingPreservesCustomWebPort(t *testing.T) {
	s, d := testServer(t)
	p, err := LoadPackage(s.PackagePath)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(d.Mount)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := p.Apply(root, strings.Replace(testConfig, "web_port=80", "web_port=8080", 1)); err != nil {
		t.Fatal(err)
	}
	w := call(s, "GET", "/api/drives", nil, true)
	var rows []struct {
		Config map[string]string `json:"config"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &rows) != nil || len(rows) != 1 || rows[0].Config["web_port"] != "8080" {
		t.Fatalf("custom port missing from drive listing: %s", w.Body.String())
	}
}
func TestLinuxDiscovery(t *testing.T) {
	data := []byte(`{"blockdevices":[{"name":"/dev/sda","type":"disk","tran":"usb","rm":true,"ro":false,"size":8000000000,"serial":"one","pttype":"dos","log-sec":512,"children":[{"name":"/dev/sda1","type":"part","partn":1,"fstype":"vfat","fsver":"FAT32","mountpoints":["/media/user/USB"]}]},{"name":"/dev/sdb","type":"disk","tran":"usb","rm":true,"size":8000000000,"log-sec":512,"children":[{"mountpoints":["/"]}]},{"name":"/dev/nvme0n1","type":"disk","tran":"nvme","rm":false}]}`)
	ds, e := linuxDrives(data)
	if e != nil || len(ds) != 1 || !ds[0].Eligible {
		t.Fatalf("discovery: %+v %v", ds, e)
	}
	other := bytes.Replace(data, []byte(`"serial":"one"`), []byte(`"serial":"two"`), 1)
	changed, _ := linuxDrives(other)
	if ds[0].ID == changed[0].ID {
		t.Fatal("hardware identity not bound")
	}
	for _, bad := range []string{"exfat", "FAT16"} {
		d, e := linuxDrives(bytes.ReplaceAll(data, []byte("FAT32"), []byte(bad)))
		if e != nil || d[0].Eligible {
			t.Fatal("non-FAT32 accepted")
		}
	}
}
func TestWindowsDiscoveryAndFormatPlan(t *testing.T) {
	data := []byte(`[{"device":"3","serial":"USB123","name":"USB","size":64000000000,"style":"GPT","sector":512,"parts":[{"number":1,"letter":"E","fs":"exFAT"}]}]`)
	ds, e := windowsDrives(data)
	if e != nil || len(ds) != 1 || ds[0].Eligible {
		t.Fatal(ds, e)
	}
	p, _ := LoadPackage(fixtureZip(t, fixtureFiles()))
	d := ds[0]
	ready := d
	ready.Eligible = true
	ready.Mount = t.TempDir()
	commands := []string{}
	run := func(_ context.Context, name string, args []string, _ string) ([]byte, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	e = nativeFormat(context.Background(), d, p, testConfig, "windows", run, func(context.Context) ([]Drive, error) { return []Drive{ready}, nil })
	if e != nil {
		t.Fatal(e)
	}
	if len(commands) != 1 || !strings.Contains(commands[0], "-Size 30GB") || !strings.Contains(commands[0], "-PartitionStyle MBR") || !strings.Contains(commands[0], "$d.IsBoot") {
		t.Fatal(commands)
	}
}
func TestDownloadsAndSelfUpdate(t *testing.T) {
	pack := fixtureZip(t, fixtureFiles())
	payload, _ := os.ReadFile(pack)
	binaryPath, _ := os.Executable()
	binary, _ := os.ReadFile(binaryPath)
	name := BinaryName(runtime.GOOS, runtime.GOARCH)
	recoveryFiles := map[string][]byte{"JBLToolkit/RECOVER": []byte("RECOVER\n")}
	all := fixtureFiles()
	for _, n := range []string{entry, "JBLToolkit/launch.sh", "JBLToolkit/common.sh", "JBLToolkit/recover.sh", "JBLToolkit/dispatch.sh", "JBLToolkit/BOOTSTRAP.SHA256SUMS", "README.txt", "LICENSE.txt"} {
		recoveryFiles[n] = all[n]
	}
	recoveryBytes, e := os.ReadFile(fixtureZip(t, recoveryFiles))
	if e != nil {
		t.Fatal(e)
	}
	contents := map[string][]byte{"jbl-bar-toolkit-v0.3.0-dev.10-usb.zip": payload, "jbl-bar-toolkit-v0.3.0-dev.10-recovery.zip": recoveryBytes, "release.json": []byte(`{"schema":1,"version":"0.3.0-dev.10","target":"auto","profile":"bar91-23.06.41.80"}`), name: binary}
	var manifest strings.Builder
	for n, b := range contents {
		fmt.Fprintf(&manifest, "%x  %s\n", sha256.Sum256(b), n)
	}
	contents["SHA256SUMS"] = []byte(manifest.String())
	corrupt := false
	var host string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases" {
			assets := []Asset{}
			for n, b := range contents {
				assets = append(assets, Asset{Name: n, URL: host + "/" + n, Size: int64(len(b))})
			}
			json.NewEncoder(w).Encode([]Release{{Tag: "v0.3.0-dev.10", Published: time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC), Assets: assets}})
			return
		}
		b, ok := contents[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if corrupt && r.URL.Path != "/SHA256SUMS" {
			b = []byte("corrupt")
		}
		w.Write(b)
	}))
	defer server.Close()
	host = server.URL
	d := &Downloads{Client: server.Client(), Cache: t.TempDir(), API: host + "/releases"}
	r, e := d.Latest(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if !CanUpdate("0.3.0-dev.9", r) || CanUpdate("0.3.0-dev.11", r) || CanUpdate("dev", r) {
		t.Fatal("invalid version comparison")
	}
	p, recovery, e := d.Toolkit(context.Background(), r)
	if e != nil || p == "" || recovery == "" {
		t.Fatal(p, recovery, e)
	}
	target := filepath.Join(t.TempDir(), "setup")
	os.WriteFile(target, []byte("old executable"), 0755)
	if e = d.UpdateSelf(context.Background(), r, target); e != nil {
		t.Fatal(e)
	}
	got, _ := os.ReadFile(target)
	if !bytes.Equal(got, binary) {
		t.Fatal("self update did not replace temp executable")
	}
	corrupt = true
	d.Cache = t.TempDir()
	if _, e = d.Asset(context.Background(), r, name); e == nil {
		t.Fatal("bad checksum accepted")
	}
	if _, e = parseChecksums([]byte(strings.Repeat("0", 64) + "  ../escape\n")); e == nil {
		t.Fatal("unsafe checksum path")
	}
}
func TestBoundedHTTPAndOrigins(t *testing.T) {
	s, _ := testServer(t)
	r := httptest.NewRequest("POST", s.Origin+"/api/prepare", strings.NewReader("{}"))
	r.Header.Set("X-Session-Token", s.Token)
	r.Header.Set("Origin", "https://attacker.invalid")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin request accepted")
	}
	w = call(s, "POST", "/api/prepare", map[string]any{"Config": strings.Repeat("x", 20000)}, true)
	if w.Code != 400 {
		t.Fatal("large request accepted")
	}
	w = call(s, "GET", "/", nil, false)
	data, _ := io.ReadAll(w.Result().Body)
	if !bytes.Contains(data, []byte("Set up or update your bar")) || w.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("embedded interface missing")
	}
}

func TestMacDiscoveryBootProtection(t *testing.T) {
	for _, fat := range []string{"Windows_FAT_32", "DOS_FAT_32"} {
		t.Run(fat, func(t *testing.T) { testMacDiscoveryBootProtection(t, fat) })
	}
}

func testMacDiscoveryBootProtection(t *testing.T, fat string) {
	encode := func(v any) []byte {
		b, e := plist.Marshal(v, plist.XMLFormat)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	run := func(_ context.Context, name string, args []string, _ string) ([]byte, error) {
		key := strings.Join(args, " ")
		switch key {
		case "list -plist external physical":
			return encode(map[string]any{"AllDisksAndPartitions": []any{map[string]any{"DeviceIdentifier": "disk3", "Content": "FDisk_partition_scheme", "Size": uint64(8 << 30), "Partitions": []any{map[string]any{"DeviceIdentifier": "disk3s1", "Content": fat, "MountPoint": "/Volumes/JBL", "VolumeName": "JBL"}}}, map[string]any{"DeviceIdentifier": "disk0", "Content": "FDisk_partition_scheme", "Size": uint64(8 << 30)}}}), nil
		case "info -plist /":
			return encode(map[string]any{"ParentWholeDisk": "disk4", "APFSContainerReference": "disk4"}), nil
		case "apfs list -plist":
			return encode(map[string]any{"Containers": []any{map[string]any{"ContainerReference": "disk4", "PhysicalStores": []any{map[string]any{"DeviceIdentifier": "disk0s2"}}}}}), nil
		case "info -plist disk3":
			return encode(map[string]any{"BusProtocol": "USB", "RemovableMedia": true, "Writable": true, "DeviceBlockSize": 512, "DeviceTreePath": "USB/fixture", "MediaName": "Fixture"}), nil
		default:
			t.Fatalf("unexpected command %s %s", name, key)
			return nil, nil
		}
	}
	ds, e := darwinDrives(context.Background(), run)
	if e != nil || len(ds) != 1 || !ds[0].Eligible || ds[0].Device != "/dev/disk3" {
		t.Fatal(ds, e)
	}
}

func TestLocalRecoveryDiscovery(t *testing.T) {
	pack := fixtureZip(t, fixtureFiles())
	all := fixtureFiles()
	files := map[string][]byte{"JBLToolkit/RECOVER": []byte("RECOVER\n")}
	for _, n := range []string{entry, "JBLToolkit/launch.sh", "JBLToolkit/common.sh", "JBLToolkit/recover.sh", "JBLToolkit/dispatch.sh", "JBLToolkit/BOOTSTRAP.SHA256SUMS", "README.txt", "LICENSE.txt"} {
		files[n] = all[n]
	}
	recovery := fixtureZip(t, files)
	data, err := os.ReadFile(recovery)
	if err != nil {
		t.Fatal(err)
	}
	adjacent := filepath.Join(filepath.Dir(pack), "toolkit-recovery.zip")
	if err = os.WriteFile(adjacent, data, 0600); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer("dev", "fixture-executable", pack)
	if err != nil || server.RecoveryPath != adjacent {
		t.Fatal(server, err)
	}
	if err = os.WriteFile(adjacent, []byte("corrupt recovery"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = NewServer("dev", "fixture-executable", pack); err == nil {
		t.Fatal("mismatched recovery accepted")
	}
}

func TestRejectedExecutableKeepsOriginal(t *testing.T) {
	data := []byte("not an executable")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer server.Close()
	name := BinaryName(runtime.GOOS, runtime.GOARCH)
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	release := &Release{Tag: "v0.3.0-dev.10", Assets: []Asset{{Name: name, URL: server.URL, Size: int64(len(data))}}, Checksums: map[string]string{name: digest}}
	downloads := &Downloads{Client: server.Client(), Cache: t.TempDir(), API: server.URL}
	target := filepath.Join(t.TempDir(), "setup")
	if err := os.WriteFile(target, []byte("original"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := downloads.UpdateSelf(context.Background(), release, target); err == nil {
		t.Fatal("invalid executable accepted")
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "original" {
		t.Fatal("failed update modified original", err)
	}
}
func TestFormattingJobValidationBeforeDiscovery(t *testing.T) {
	job := filepath.Join(t.TempDir(), "job.json")
	for _, data := range [][]byte{[]byte("{}"), bytes.Repeat([]byte("x"), 16385), []byte(`{"Config":"action=$(id)"}`)} {
		if err := os.WriteFile(job, data, 0600); err != nil {
			t.Fatal(err)
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(data))
		if err := RunFormatJob(job, digest); err == nil {
			t.Fatal("invalid formatting job accepted")
		}
		if err := RunFormatJob(job, strings.Repeat("0", 64)); err == nil {
			t.Fatal("changed formatting job accepted")
		}
	}
}

func TestDownloadKeepsNewerLocalPackage(t *testing.T) {
	s, _ := testServer(t)
	original := s.PackagePath
	var host string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases" {
			json.NewEncoder(w).Encode([]Release{{Tag: "v0.3.0-dev.9", Published: time.Now(), Assets: []Asset{{Name: "jbl-bar-toolkit-v0.3.0-dev.9-usb.zip", URL: host + "/usb"}, {Name: "SHA256SUMS", URL: host + "/checksums"}}}})
		} else {
			fmt.Fprintf(w, "%s  release.json\n", strings.Repeat("0", 64))
		}
	}))
	defer server.Close()
	host = server.URL
	s.Downloads = &Downloads{Client: server.Client(), Cache: t.TempDir(), API: host + "/releases"}
	result := call(s, "POST", "/api/download", map[string]any{}, true)
	if result.Code != 400 || !strings.Contains(result.Body.String(), "older") || s.PackagePath != original {
		t.Fatal("local package was downgraded", result.Body.String())
	}
}

func TestLocalSourceNeverDownloadsPublishedPayload(t *testing.T) {
	s, _ := testServer(t)
	s.LocalSource = true
	s.Version = "0.3.0-dev.10"
	original := s.PackagePath
	result := call(s, "POST", "/api/download", map[string]any{}, true)
	if result.Code != 400 || s.PackagePath != original || !strings.Contains(result.Body.String(), "local files") {
		t.Fatal("source setup downloaded a release", result.Body.String())
	}
	info := call(s, "GET", "/api/info", nil, true)
	if !strings.Contains(info.Body.String(), `"localSource":true`) || !strings.Contains(info.Body.String(), `"canUpdateSelf":false`) {
		t.Fatal("local source mode not advertised")
	}
	update := call(s, "POST", "/api/self-update", map[string]any{}, true)
	if update.Code != 400 {
		t.Fatal("repository mode allowed self-update")
	}
}

func TestDriveReportsIncludeRecentSetupFailure(t *testing.T) {
	s, d := testServer(t)
	dir := filepath.Join(d.Mount, "JBLToolkit")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "native-check.json"), []byte(`{"power":"Standby","error":"turn the soundbar fully on before installation"}`), 0600); err != nil {
		t.Fatal(err)
	}
	log := strings.Repeat("old output\n", 7000) + "SPACE_REQUIRED_KIB=9472 SPACE_AVAILABLE_KIB=8856\nPREFLIGHT_REASON=FREE_SPACE_UNREADABLE_OR_INSUFFICIENT\n"
	if err := os.WriteFile(filepath.Join(dir, "setup.log"), []byte(log), 0600); err != nil {
		t.Fatal(err)
	}
	w := call(s, "GET", "/api/drives", nil, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "SPACE_REQUIRED_KIB=9472") || !strings.Contains(w.Body.String(), "FREE_SPACE_UNREADABLE_OR_INSUFFICIENT") {
		t.Fatal(w.Body.String())
	}
	var rows []struct {
		Reports map[string]string `json:"reports"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || len(rows[0].Reports["setup.log"]) != 65536 || !strings.Contains(rows[0].Reports["native-check.json"], "turn the soundbar fully on") {
		t.Fatal("unbounded or missing setup log")
	}
}
