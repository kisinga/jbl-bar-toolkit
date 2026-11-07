# Development

Keep scripts readable, packages reproducible and recovery working. Preserve firmware identities, native guards and existing startup hooks. Supporting another build requires field, event ABI and restoration validation; changing a hash is insufficient.

## Build and test

The [root README](../../../README.md#run-from-this-repository) and
[contributor guide](../../../CONTRIBUTING.md) define the current Go setup/build
commands and test requirements. The commands below are retained for standalone
maintenance, private helper builds and historical fixture verification.

For optional extracted ARM BusyBox descriptor checks, install `qemu-arm` and registered ARM binfmt, then run:

```sh
JBL_ARM_BUSYBOX=/absolute/path/to/busybox python3 tests/bar91/check_descriptors.py
```

Supply a legally obtained binary; vendor binaries are not bundled.

## Layout

| Path | Contents |
| --- | --- |
| `apps/remote/` | Go server and embedded browser interface |
| `models/bar91/smart-mode/` | Exact Smart Mode installable source, including CRLF launcher/hook |
| `models/bar91/ssh/` | Portable SSH setup templates |
| `models/bar91/usb/` | Combined USB manager, strict configuration parser and independent recovery |
| `tools/`, `tests/` | Builders, CLI, fixtures and integrity baselines |
| `research/README.md` | Findings and private archive index |

Keep generated credentials/packages, firmware, full captures, personal configuration and tool caches out of commits/releases. Use the issue template for hardware results. Root instructions cover use, recovery and community links; `USB-README.txt` and `ROLLBACK.txt` ship with Smart Mode.

Preserve the confirmed revision 5 download at `v0.1.0`. New payloads require checksums, ownership/upgrade handling, fixtures and fresh hardware evidence. Keep experimental latency changes on their branch until validated.

## Combined USB toolkit

Use the [root setup guide](../../../README.md) for the supported workflow.
The device manager shares strict parsing, firmware/storage checks, one startup
dispatcher and independent recovery. SSH's managed mode preserves toolkit hook
ownership; automation-only mode runs without HTTP or discovery.

`tools/build_usb.py` remains the independent private builder used by host fixtures
and native helper development. It is not required by setup or CI packaging.
Run physical qualification before publishing; passing host checks is insufficient.

## SSH package

Hardware evidence covers USB-free startup, root password login, an interactive terminal and SFTP on the development bar. Independently validate a clean portable installation before calling it confirmed. Reuse native vendor binaries; do not distribute their loader/libraries.

Build on Linux with Python 3:

```sh
python3 tools/build_ssh.py --allowed-pc <computer-ipv4-address>
```

Enter a new root password twice, at least eight characters. The builder embeds its SHA-512 crypt hash and limits login to the supplied computer address. `--allowed-pc` also accepts a private IPv4 CIDR such as `192.168.1.0/24` to allow any host on that subnet; the bar’s own address on the chosen interface must lie inside it. Keep generated packages private, including password hashes and derived Wi-Fi PSKs.

Default behavior reuses configured Wi-Fi and enables SSH autostart. Add `--wifi` for interactive SSID/WPA2 credentials or `--usb-only` to require USB insertion after each reboot. The default interface is `wlan0`.

1. Keep `dist/jbl-bar9-ssh-rollback-private.zip` on the computer.
2. Extract `dist/jbl-bar9-ssh-private.zip` directly to FAT32 USB, with `AutoScript/AutoScript` and `JBLLocalSSH/` at its root. Use one AutoScript package at a time.
3. Insert into the fully on, normally playing bar. Leave controls alone for three minutes, then return USB to the computer.
4. Require `JBLLocalSSH/STATUS.txt` to report `SSH_READY` or `ALREADY_RUNNING_PASSWORD_UNCHANGED`; inspect `setup.log`. Reinsertion preserves an already-running daemon’s password.
5. Verify the fingerprint in `connection.txt` against `host_ed25519.pub` before accepting the SSH host key.
6. Test `ssh -p 2222 root@<bar-ip-address>`, a normal command, an interactive terminal and SFTP. A listener or `sshd -t` alone is insufficient.
7. With recovery available, restart **without USB**. Verify SSH returns and LOAD, power, volume and standby remain normal.

Remove browser remote first. To disable SSH autostart, run `/bin/sh /var/JBLLocalSSH/rollback-boot.sh` over SSH, or use the matching rollback ZIP on clean FAT32 USB. Rollback restores the recorded pre-SSH hook only when recognized; active sessions, stored files and unknown later hook edits are preserved.

Preserve inherited-socket cleanup, background startup, bounded Wi-Fi wait and the Smart Mode hook. Native OpenSSH requires the compatible private loader/NSS/account view, including host-key generation and session commands.

## Browser remote

Requirements: working root SSH, Python 3 and Go 1.22+ on the computer (tested Go 1.25.5). The bar needs no Go runtime, external Go modules, database or ongoing computer service.

```sh
python3 tools/build_web.py --allowed-cidr <trusted-lan-subnet>
(cd apps/remote && go test -race ./... && go vet ./...)
```

Use the actual trusted IPv4 subnet, such as `192.168.1.0/24`. Output goes to `dist/webremote-private/`. The builder strips local source paths and VCS metadata; identical inputs/toolchains produce identical binaries. Configuration varies by owner. The normal Go-only build disables Bluetooth transport controls and Front EQ.

### Optional Go wake automation

Add `--auto-standard` to the build command to let the Go service select Standard Mode after wake. Keep the Bluetooth/EQ helper flags if those features are needed:

```sh
python3 tools/build_web.py --allowed-cidr <trusted-lan-subnet> --auto-standard \
  --bluetooth-helper build/bt-remote --eq-parser build/eq-parser
```

Add `--smart-mode-memory` as well to restore the mode chosen before standby instead of always selecting Standard ([USB setting](USB-TOOLKIT.md) `smart_mode_memory`). The wake then completes without a gesture when Smart is remembered (status `SMART_KEPT_THIS_WAKE`, `remembered_mode` in the automation state). A mode is remembered once the wake is handled and it has stayed awake with idle keys on the same firmware process: five one-second samples for the mode selected from the page or Home Assistant, sixty for any other change. On 4 Nov 2026 the firmware switched to Smart about 25 s before reporting standby after the power button, which a short window took for a choice; it is written to `/var/JBLRemote/smart-mode.json` only when it changes. A Standard or Smart selection from the page or Home Assistant before the automation acts completes the wake (`MANUAL_MODE_THIS_WAKE`): no gesture follows it, and it becomes the remembered mode. While the wake is checked and Standard restored, the page disables its controls with an explanation, since the service refuses native controls while sound settings settle. Device record: [smart-mode-memory-device.json](../../../models/bar91/validation/smart-mode-memory-device.json) (Standard restored after two standby/wake cycles, Smart kept from the record after a restart).

Go shares native controls, locking and readback with the web interface. An OS-held ownership lock excludes duplicate Go automation and recovery commands. It samples readiness every second, requires three stable awake samples, and acts once per wake. Standby has no deadline. Manual Smart selection is respected until the next standby or service/firmware restart. A failed gesture stops automatic attempts for that wake and leaves the reason in the page’s device details and `remote.log`.

An installed revision 5/6 shell helper is verified, paused with an owned `DISABLED` marker and retained intact. Unknown/modified helpers and existing manual disable flags are preserved. No legacy helper is required if SSH and the web service are already available. The original button sequence is still used; this is not a direct native Smart Mode setter.

Set `auto_standard` to `false` in `/var/JBLRemote/config.json` and restart the service to return automation to the shell helper. Normal service shutdown and remote uninstall also restore only the verified helper paused by Go. For recovery after an interrupted removal, run `/var/JBLRemote/remote -restore-smart-helper` with the service stopped. Do not delete an unknown disable flag. Removing the USB Smart Mode package alone does not disable Go’s separately enabled automation.

Volume/mute are checked against the state immediately before the gesture. The firmware can clear mute itself on power-on. Keep CEC, other input layouts and mains cold boots separate from the measured standby/wake tests ([device record](../../../models/bar91/validation/go-automation-device.json)).

### Optional Bluetooth helper

Use an ARM hard-float GNU/Linux GCC toolchain and an extracted copy of the supported firmware:

```sh
python3 tools/build_bt.py --cc /path/arm-none-linux-gnueabihf-gcc \
  --rootfs /path/extracted/rootfs --output build/bt-remote
python3 tools/build_web.py --allowed-cidr <trusted-lan-subnet> \
  --bluetooth-helper build/bt-remote
```

Only the project’s fixed-command helper is embedded. Matching vendor libraries are checked locally and reused from the bar; do not redistribute them. The helper registers no event subscribers. The server checks firmware, current input and connected peer before sending playback requests.

Audible Pause and playback resumption are confirmed; Next/Previous/Stop remain unconfirmed. One Play/Pause button follows successful Bluetooth requests because A2DP activity cannot establish app playback. Native stream, source or peer changes reset intent; errors never flip it. Normal Cast controls require a nonmirrored session and advertised capabilities, with hardware validation still outstanding.

### Optional Front EQ helper

Use the same ARM hard-float GNU/Linux GCC toolchain and supported extracted firmware as the Bluetooth helper:

```sh
python3 tools/build_eq.py --cc /path/arm-none-linux-gnueabihf-gcc \
  --rootfs /path/extracted/rootfs --output build/eq-parser
python3 tools/build_web.py --allowed-cidr <trusted-lan-subnet> \
  --bluetooth-helper build/bt-remote --eq-parser build/eq-parser
```

Omit `--bluetooth-helper` if playback controls are not needed. Only project helper code is embedded; vendor libraries stay on the bar. Both build-time and runtime hash checks reject unsupported libraries.

Turn on Standard Mode and start audio. Select a preset or adjust the five sliders, then **Apply EQ**; **Reset** returns to original tuning. Presets are starting points rather than measured corrections: Original, Less bass, Clear dialogue, Softer treble, Warm, Bright and Vocal focus. They contain relative cuts only, not another device’s calibration. Front LR adjustment covers 125/500/2,000 Hz bells and overlapping 4,000/8,000 Hz high shelves, −6 to 0 dB in 0.5 dB steps. It does not tune the subwoofer, rears or height channels.

Application uses native custom DSP mode 5. It is not proven equivalent to stock processing plus EQ. Each request validates a complete 3,840-byte tuning reconstruction with the native parser before applying it; unknown calibration differences fail closed. Reset uses the native 1 → 0 preset transition and checks exact original bytes. Keep the bar’s normal mute state across both operations. The mute state is restored 0.8 s after the new mode reads back (the DSP rebuilds its filters then, and an unmute sent during the rebuild could be lost while the mute flag already read unmuted), with up to three attempts and a re-read of the flag; a restore that still fails says so and asks to press Mute. The restore has its own 9 s budget, independent of the request deadline, and runs before the action returns, so the action gate stays held until it finishes; EQ actions (namespaced and `eq_apply`/`eq_reset`) get a 20 s reply-timeout from the Bar 9.1 composition so the reply covers it.

An applied EQ is kept until **Reset** ([device record](../../../models/bar91/validation/front-eq-keep-device.json)). Its bands are saved in `/var/JBLRemote/front-eq.json`. With an EQ applied, the firmware recomputes the custom block for new bass, Atmos and rear levels (byte-identical to the same EQ applied directly at those levels) and leaves night mode untouched, so these controls no longer reset it first; the monitor accepts the recomputed block when the levels changed. A source change or Smart Mode makes the firmware return to the native preset; source and Smart/Standard controls therefore no longer reset it first either, and the monitor finishes the cleanup without muting, then applies the saved EQ again after three stable checks of Standard playback on the qualified route (about 2.2 s muted). The same happens after wake and restart. A reset after level changes confirms the native preset by mode and cache instead of comparing with the baseline of other levels. Power from on and the setup controls still reset first; the saved EQ is applied again afterwards. While an EQ is applied, the bass offset monitor stays paused, as before. Other native controls, standby/CEC and HDMI/eARC/Cast layouts still need qualification; Bluetooth has audible confirmation. Recovery files belong in `/var/JBLRemote`; preserve them after an error and try **Reset**. Do not edit another program’s `/mnt/ubi_boot/temp.json` or remove recovery files to force an unsupported route.

The bar often leaves the qualified EQ route by itself, for example when Smart Mode returns after wake and HDMI Dolby then uses the film page `0x840002`. Dolby alone does not: on 7 Nov 2026, HDMI in Standard Mode with `DOLBY SURROUND DD+` (5.1 from an Nvidia Shield) kept the EQ applied and confirmed (`status: active`). On 4 Nov 2026, with an EQ applied on Bluetooth, the bar on HDMI in Smart Mode read native mode 2 and cache 2 on page `0x840002`: the custom mode (5) no longer played, yet the pending reset refused every native control, Standard included. When the full reader rejects the route, a narrow read-only reader of the HPP mode now decides. If the mode is not 5, controls proceed and the reset stays pending; the monitor confirms it (mode, preset and saved baseline) once Standard Bluetooth playback returns. If the mode is still 5, or cannot be read, input changes and Standard proceed, since both lead back to where Reset works, and other controls are refused with that instruction. Other reset failures still refuse the control. No new DSP write is involved.

### Install and remove

Check that no unrelated installation owns `/var/JBLRemote`. Copy the output directory over SFTP, then install:

```text
sftp -P 2222 root@<bar-ip-address>
put -r dist/webremote-private /var/JBLRemote
bye
```

```sh
ssh -p 2222 root@<bar-ip-address> '/bin/sh /var/JBLRemote/install.sh'
```

Open `http://jbl.local`, or the bar’s local IP if mDNS is blocked/conflicting. Port `8080` remains available. Existing Bonjour handles probing/conflicts; registration follows Wi-Fi address changes and withdraws on exit. No silent alias renaming occurs.

Anyone on the configured trusted subnet can use the remote without a web login. Requests must match accepted Host/local interface addresses. Active native `ap0` hotspot clients are also allowed only for that interface’s HTTP destination. Fixed commands, JSON-only POSTs, same-origin checks and CSRF tokens guard mutations; no arbitrary command/filesystem endpoint is exposed.

Installation preserves the exact startup hook and adds one background command. Remove **browser remote → SSH → Smart Mode**. To remove only the remote:

```sh
ssh -p 2222 root@<bar-ip-address> '/bin/sh /var/JBLRemote/uninstall.sh'
```

Removal stops its own process and restores the recorded hook only when recognized; files and unknown later hook changes remain. Keep SSH usable during development.

### State and interface

Prioritize listening and sound controls. Use the optional Front EQ helper for supported native routes; [EQ findings](../../../research/README.md#eq-exploration--20-october-2026) record parser, application and restoration evidence.

One native reader samples each second only while subscribed through `EventSource`; changed state is coalesced and slow clients retain only the newest update. Wi-Fi status is cached for five seconds, Cast status for two seconds while requested. The auxiliary system monitor caches two-second CPU/RAM/storage/network samples, with bounded two-minute browser history and no database. Missing sensors/readings remain unavailable. Monotonic revisions prevent older replies from overwriting newer observations. Both listeners share controls and access checks; an occupied port 80 leaves 8080 usable.

Run UI fixtures with Node and Playwright:

```sh
# First time only:
npm install --no-save --package-lock=false playwright
npx playwright install chromium
node tests/webremote-ui.mjs
```

Set `PLAYWRIGHT_MODULE=/absolute/path/to/playwright/index.mjs` to reuse an installation. The current **179 browser checks** cover queues/cancellation, held and keyboard controls, failures/reconnects, stale replies, standby, Bluetooth/streaming, single Play/Pause and intent reset after reconnect/standby, EQ presets/manual drafts/application gating, Go automation status/errors, system sample history/gaps/availability, reduced motion and 320/390/1024px layouts. Public screenshots use fixtures, never live personal data.

Direct controls use fixed native/protocol commands, fresh source/capability checks and readback. Cast acknowledgements must match the commanded media session; an empty session is accepted only after Stop. Bluetooth pairing uses `app.bt.pair`; qualified Bluetooth volume/mute uses Cast receiver volume. Cast media uses current receiver sessions; AirPlay uses the verified SDK command mapping. Uncertain requests are never retried through a second path. Keep IOM for unsupported volume routes and complete sound actions. **Do not use `app.amb.po`/`app.amb.sb`: the former crashed the native service during qualification.** See the [control qualification record](../../../models/bar91/validation/direct-controls-device.json).

Go tests cover access boundaries, event streams, native Wi-Fi datagrams, readback, peer filtering, sound decoding, Bonjour, Cast framing/status, system parsing/cache behavior, native EQ route guards and full-block recipe preservation, wake state transitions and legacy takeover/restoration. Run race tests and vet after backend changes. Fixtures do not establish audible behavior.

Hardware confirmation includes volume/mute, pairing/name/native stream states, AirPlay/Wi-Fi readback, Night Mode Off → On → Off and bass/Atmos cycles restored to initial settings. Rear changes require an undocked test; product TV/ARC, HDMI and Bluetooth selection now have a controlled roundtrip, while broader power/source/manual Standard coverage remains experimental. Chrome Audio Mirroring (`85CDB22F`) keeps receiver PLAYING when sender playback pauses; label mirroring without claiming sender pause detection. Automatic hotspot activation/access needs a separate outage test.

### Center level (experimental)

The firmware has no native center setting. `center_next` cycles five levels by writing the per-channel digital gain of the TAS5825M at 8-bit address `0x98` (left = center tweeter, right = center driver): book `0x8C`, page `0x0B`, registers `0x0C`/`0x10`, Q9.23 big-endian (`0x00800000` = 0 dB). Transfers use bdpprog's `/dev/i2c` write ioctl `0x40047301` and 36-byte descriptor (bus `0x63/0x62`), always ending on book 0 / page 0. Levels 0–5 map to 0, +2.4, +4.8, +7.2, +9.6 and +12 dB (boost only); level 0 writes nothing until another level has been chosen. +6 dB was validated by ear; above it, loud passages can clip, so lower other volume rather than pushing the top levels at high volume.

Amplifiers reload defaults on wake, so a saved non-default level (`/var/JBLRemote/center-level`) is re-applied 4 s and 15 s after each observed wake or firmware restart, only with idle keys and no other control running. Front EQ is not reset by this control (different processing stage). Limits: the read ioctl fails on the qualified unit, so the page shows the requested level; bdpprog shares the device and could interleave its own amplifier writes during the short book/page window. Channel identification and audible +6 dB dialogue boost were checked by ear on one bar; standby/wake persistence needs further qualification.

### Optional CLI

Use Python’s standard library against the same deployed API:

```sh
python3 tools/jbl.py status
python3 tools/jbl.py volume up
python3 tools/jbl.py night on
python3 tools/jbl.py smart off
python3 tools/jbl.py --host <private-ipv4[:port]> status --json
python3 tests/check_cli.py
```

Also supported: `volume down`, `mute`, `power`, `source tv|hdmi|bluetooth`, and available `play`, `pause`, `next`, `previous`, `stop`. `--timeout` accepts 1–30 seconds. JSON excludes CSRF tokens. Night/Smart Mode commands check known state before changing it; unsupported playback is refused and failures are never retried automatically. Manual Smart Mode off preserves the persistent watcher.

[k1rnt/JBL Soundbar CLI](https://github.com/k1rnt/jbl-soundbar-cli/tree/7d10bdb1ebe0c5a77c9a4c14ebe4580bd3735309) supplied CLI/control inspiration. Its newer Linkplay/JBL One transport is separate; this CLI uses the fixed Bar 9.1 Go endpoints.

## Faster wake — revision 6

Build revision 6 from `main`; the former `work/faster-wake` branch is merged. Keep the original v0.1.0/revision 5 release and checksums as the recovery baseline.

| Setting | Revision 5 | Revision 6 |
| --- | --- | --- |
| Watcher polling | 5 seconds | 1 second |
| Identical awake samples | 3 | 3 |
| Runner initial delay | 10 seconds | 1 second |
| Readiness sampling | 2 seconds / 3 samples | Unchanged |
| Mute hold, release/completion guards | Original controller | Unchanged |
| Standby deadline | None | None |

Measured on the same owned bar on 20 October 2026, from first observed awake state to confirmed Standard Mode:

| Test | Seconds |
| --- | --- |
| Revision 5 control | 34.64 |
| One-second polling, original initial delay | 27.00 |
| Revision 6, 23-second standby | 18.26 |
| Revision 6, 9.6-second standby | 18.01 |
| Revision 6, 76.1-second standby | 17.16 |

Require released keys, exact console restoration and unchanged volume/mute/source/sound settings; a display message alone is insufficient. Raw logs remain private; [device validation](../../../models/bar91/validation/revision6-device.json) records sanitized results. CEC wakes, mains cold boots and additional bars still need qualification.

One unexpected reboot occurred during an initial standby test before any Standard Mode sequence ran. The previous log ended in native power-off state `power=0/system=1`; subsequent revision 5 control and faster-wake tests completed normally. The cause remains unresolved; do not attribute it to the timing change or claim it fixed.

```sh
python3 tools/test.py
# ZIPs: dist/jbl-bar9-standard-mode-v0.2.0-dev.1-{install,uninstall}.zip
```

Use the matching revision 6 uninstall package, not the published v0.1.0 uninstaller. Exact revision 5 payloads/hooks may upgrade through the USB installer; unknown files and modified hooks remain protected. With SSH/web startup hooks already added, the USB upgrader intentionally refuses that modified hook. Preserve the entire helper and hook before a controlled SSH payload update; do not overwrite the combined startup hook with `hook.expected`.

The tested SSH update replaced only the owned Smart Mode payload, preserved the combined startup hook byte for byte, and retained the prior helper for rollback. Installation from USB and removal of the new version still need independent hardware tests. The v0.1.0 downloads are unchanged.


## Advanced controls

Ported from merged PR #5 (upstream `3a52f58`). Hardware observations below are
reported upstream evidence; this reorganization adds no new physical result.


`smart` reuses the qualified Standard gesture (Mute hold, then +) and is accepted only from Standard; read-back requires mode Smart and unchanged volume/mute. The bar returns to Smart after power-on, so a selected Standard does not survive standby.

`av_sync_set` with `"value": <ms>` follows the manual's audio sync gesture: hold the remote's TV key `0x1f02c` for 3.5 s and release: the bar opens sync mode and shows the current delay; then + or − per 10 ms step. The saved delay is config `375` (ms, 0–250; + stops at 250 on the qualified bar), but the bar only saves it when sync mode closes: during the gesture each step is confirmed with the delay the display shows (`0xd8a088 + 0xee`, e.g. `  20MS`, polled for up to 1.5 s), which must equal config 375 right after the hold; config 375 must then reach the target within three seconds after sync mode has closed. A press that does not change the value is retried once (the bar can drop a press that follows another closely); an unchanged value at 0 or 250 ms, or after the retry, stops the request. Every step, not only the first, must leave the volume unchanged. The hold alone shows the audio sync message (display message 26, see below), which must appear within a second of the release before any + is sent; otherwise nothing else is sent. If the first step still changes volume, sync mode did not open: the volume step is undone and nothing else is sent. (An earlier qualification tool sent the next key while the hold was still running, so it reached the bar exactly at release and only showed the delay; with each key acknowledged, the first press after the hold changes the delay.) The bar treats +/− as sync changes for five seconds after the last press, so once the hold has been sent the request keeps the native lock until that window closes on every exit, including errors and an expired request (the wait does not use the request context). Each key of these long-press controls (audio sync, calibration, subwoofer pairing) is followed by the existing acknowledgement marker, queued behind the key and awaited for longer than the hold, before the key-release check: CLI acceptance alone does not prove the firmware ran the key, and the keys still look idle before a hold starts. Limits: TV/ARC and HDMI inputs only (both kept their input during qualification; Bluetooth and streaming untested), the input must be unchanged afterwards, any change in one request and one sync session (no closing and reopening between steps, so the display stays on), about 4 s plus 0.8 s per step plus the 5.5 s close; the request has a 60 s limit and extends the write deadline of its own reply beyond the server's default 20 s.

Hardware evidence (2 Nov 2026, HDMI input, config 365 = 3 before and after): Smart ↔ Standard three times with read-back and restored console; audio sync 0 → 20 → 0 ms and 0 → 250 (limit) → 0 ms with config 375 read-back and no other setting changed. `0x1f00c` (TV input key) does not open sync mode; `0x1f00e` selects the TV input. Holding Atmos for 11 s showed NOT SUPPORTED on the TV input in Smart Mode (DTS Neural:X control not qualified).

Display state (`setup.go`): firmware 23.06.41.80 keeps the front display's message selectors in a static VFDMG structure at `0xc65494`. Offset `0x1c` is the temporary message (117 = none, five seconds each), offset `0x10` the pairing overlay (18 = none, 17 = subwoofer pairing). The text of the last audio stream message is at `0xd8a088 + 0x10a` (64 bytes) and is reported as `sound.audio_format` while the bar is on, only on TV/ARC and HDMI and only once the text has changed on the current source: the bar shows no stream format for Bluetooth and keeps the previous text in memory (observed: `DOLBY SURROUND DD+` still present over Bluetooth). All reads are read-only and use the existing identity check.

`calibrate` holds HDMI (`0x5f03e`) for six seconds, the manual's calibration gesture, then requires message 58 (`1-CALIBRATION`), 59 (`2-CALIBRATION`) or the countdown within three seconds; otherwise it reports that nothing started. The bar chooses the step itself and runs it alone (about 35 s), so the request returns once the step has started and `setup.calibration` reports progress: countdown 64…60, `CALIBRATING` 66, then 67/68 (step done), 69 (retry) or 65 (cancelled). Both rear speakers must be undocked. While messages 58–66 are shown, every control is refused because native keys would interrupt the measurement.

`sub_pair` holds Bass (`0x1f030`) for six seconds, releases, then presses −, as the manual describes, and requires the pairing overlay (17) within three seconds. If it does not appear, − reached the volume: the step is undone and an error is returned. Bass level, volume and mute must be unchanged afterwards. `setup.sub_pairing` stays true while the bar waits (up to three minutes); the subwoofer's rear button must be held until it flashes white quickly, then `setup.subwoofer` shows Connected (message 3).

Hardware evidence (2 Nov 2026, read with `jblkey.sh watch`): calibration step 1 `58 → 64…60 → 66 (20 s) → 67`, step 2 two minutes later `59 → 64…60 → 66 → 68`, each about 34 s, rear speakers undocked. Subwoofer: the Bass-hold-then-− gesture set overlay 17 without touching the subwoofer; holding the subwoofer button then gave message 3 and overlay 18. Audio format read `DOLBY SURROUND DD+` while a TV programme played. Holding the TV key `0x1f02c` for 3.5 s without + showed message 26 (AUDIO_SYNC) for five seconds.
