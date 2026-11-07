# Remote UI feature map

All 156 existing IDs retain their targets. Actions, options and ranges are checked
against the [frozen baseline](../tests/fixtures/remote-ui-baseline.json) by
`python3 tests/check_remote_ui.py`; browser tests verify behavior.

| Location | Existing features retained |
| --- | --- |
| Header | Model/home, connection, refresh, power/standby |
| Speaker view | Independent batteries/meters, thresholds and unavailable states; collective docking |
| Source | TV/ARC, HDMI, Bluetooth; peer, discovery and pairing |
| Volume | Tap/hold, keyboard, mute, pending values and queue cancellation |
| Playback | Service/status, metadata, progress, previous/play/pause/next/stop and source-specific availability |
| Sound | Standard/Smart, five bass levels, six center levels, Atmos/rear levels, night mode, audio format |
| Audio sync | 0–250 ms typed input or 10 ms taps; draft, debounce and source guards |
| Front EQ | Status badge (Active, Waiting with the reason, Updating, Off); eight presets, five −6..0 dB bands in 0.5 dB steps, curve, draft, one button (Apply EQ, Save EQ, or Turn off EQ for Original), kept EQ and error states; Save EQ while it cannot apply (standby, Smart Mode, unsupported route) |
| Bass offset | −3..+3 dB in 0.5 dB steps, explicit Save, startup/status/errors and permitted standby saves |
| Room setup | Two-stage calibration, subwoofer pairing, instructions and operation guards |
| System monitor | CPU/RAM history, storage, traffic, uptime/load/processes/temperature, Wi-Fi and local/hotspot addresses |
| Details/help/footer | Device identity/uptime, automation, read-only Cast tone settings, tips, feedback and freshness |

**Packaging:** default Go setup/CI builds include the verified project-owned EQ
and Bluetooth helpers. EQ still requires a supported active Standard playback
route; source-specific playback and native dependency guards remain intact.
Front-display feedback is an unavailable experimental capability until a native
entry mechanism is qualified; contract fixtures do not enable screen writes.

**Layout:** equal sound columns at ≥900px; stacked below. Audio sync, Bass offset
and System monitor start open. Other disclosures start closed. Updates preserve
manual disclosure choices; reload restores defaults. EQ and offset are gated independently.
Apricot highlights actions, violet EQ and teal diagnostics; labels and selection
markers retain meaning without color.

**Typography:** relative sizes respect browser preferences: 13px supporting text,
14–16px controls, with extra scaling at ≥1600px. Short help copy and stronger
headings preserve hierarchy; supporting text meets 4.5:1 contrast on panel backgrounds.

**State:** speaker rendering adds no connections or timers. Docking does not prove
charging; subwoofer messages are transient. Bass rings show a reported setting,
not measured audio. The existing shell, APIs and hardware guards remain unchanged.

**Screenshots:** [browser checks](../CONTRIBUTING.md#screenshots) generate the README images
from production modules and sample fixtures, never live-device captures.
