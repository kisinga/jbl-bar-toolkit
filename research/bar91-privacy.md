# Bar 9.1 telemetry and microphone audit

Investigated 5 November 2026 with REA 6.7.0 and Ghidra 12.1.2 against firmware
**23.06.41.80**. The inspected code implements telemetry, diagnostic uploads,
and local microphone recording in the calibration subsystem. **No covert
microphone-upload path or working speech-recognition model was established.**
That conclusion applies to the inspected paths and packaged resources; it is
not a complete malware clearance or a record of traffic from a soundbar.

All analysis was offline. No vendor executable was run, no device was contacted,
and no settings were changed. Actual server collection, current consent
settings, downloaded components, and runtime recording-file retention remain
unverified. This audit does not cover JBL phone apps or other soundbar models.

## Telemetry and diagnostics

| Finding | Evidence and boundary |
| --- | --- |
| Usage and performance metrics | `libmetrics.so` formats histogram, enum, sparse, user-action and crash-label records into `/data/share/chrome/metrics/uma-events`. Its “send” operations append to a local queue; they do not themselves transmit data. |
| Local metrics enabled at this layer | `MetricsLibrary::AreMetricsEnabled` returns constant true. This does not establish that an uploader ignores consent. |
| Wi-Fi events | Native `net_mgr` callers emit `Cast.Wifi.Roam` and `Cast.Wifi.Failure.Type`; the latter builds an event with name, time and numeric value, subject to state/rate conditions. |
| Router information | `net_mgr` writes AP manufacturer, model name/number, serial number, device name, vendor prefix and security mode into local properties. These writes do not establish upload of those fields. |
| Memory and security diagnostics | `process_monitor` emits memory-crash and audit events. Its report routine runs `dumpstate` through gzip and indexes dump/log paths, time, build, release and reason under `/data/chrome/minidumps`. Contents of a real generated report were not observed. |
| Google crash uploads | The packaged `crash_uploader` checks statistics opt-in. Its false branch skips upload and removes the dump/log; an eligible true branch prepares a dump and optional log for upload. The default endpoint is `https://clients2.google.com/cr/report`. |
| Crash metadata | Upload construction supports a client identifier, product/model/manufacturer/hardware/build/release details, timing, app context and reason. Optional extra metadata is supported. Field support does not prove that every field is populated or that a report was sent. |

The Cast payload also contains metric names for media readiness, playback errors,
bitrate, Wi-Fi signal/rates, network availability durations, reboot uptime/type,
multizone changes, EQ gain and HDMI CEC key presses. These are **string-level
indicators** of the shared Cast framework, rather than fully traced events for
this particular product. The executable also carries shared Assistant and other
product schemas, so their presence cannot establish enabled features.

The confirmed upload implementation belongs to Google Cast. Harman firmware
update URLs in `bdpprog` identify an update mechanism; they do not by themselves
identify a JBL telemetry receiver. No separate JBL microphone-upload endpoint
was established in the inspected paths.

## Consent handling

There are separate layers that must not be conflated:

1. The local metrics library reports metrics enabled and queues events.
2. The crash uploader explicitly checks its opt-in preference before upload.
3. The device's current preferences were not read.

`bdpprog` contains getters/setters for `persist.chrome.opt_in.stats`. Its getter
passes a default string of `"1"` to `c4a_property_get`, but the inspected
`c4a_property_get` implementation only validates/logs arguments and returns
success, without filling the output buffer. The enclosing usage getter
initializes that buffer to zero, so its normal inspected path reports false.
The setter can invoke `/system/bin/setprop`.

Consequently, neither that default-string literal nor that stub getter is
reliable evidence of the effective runtime opt-in setting. No claim of enabled
or disabled upload by default is made here. The crash preference and this
vendor wrapper were not proven to have identical storage or runtime behavior.

## Microphone recording

REA decompilation of `libhdsp.so` establishes recording buffers and disk writes
in the calibration processing path. `Process` handles calibration modes 1 and
2 and calls two dump functions after their calibration-result handling. The
inspected branches reset calibration mode and invoke the dump without a
separate debug-enable check.

The dump functions write:

- `/mnt/ubi_boot/dumpLisMicL.wav`
- `/mnt/ubi_boot/dumpLisMicR.wav`
- `/mnt/ubi_boot/dumpSurMicL.wav`
- `/mnt/ubi_boot/dumpSurMicR.wav`

Despite their `.wav` suffix, these functions write sample buffers directly using
`fopen("wb")`, `fwrite` and `fclose`, without writing a RIFF/WAVE header. The
inspected caller requests 577,536 four-byte elements per file, or **2,310,144
bytes** if the write succeeds. Sample rate and recording duration were not
established from that count.

The traced static chain connects the main application to those dumps:
`audio_cal1/2_process` sends calibration mode 1/2 through PPC parameter
`0x840003`; `libHPP.so` maps that parameter to `SetParam(0x10010077)` in
`libhdsp.so`, which sets the mode used by `Process`. This connects ordinary
calibration handlers to a local recording capability. It does not establish
that these files currently exist on a device or how long they survive.

The inspected dump functions do not call networking APIs. No path connecting
these files to the crash uploader or another network sender was established.
Generic microphone controls, PCM upload/callback plumbing, and factory audio
dump commands also occur in the inherited MediaTek stack. The word “upload” in
that stack does not by itself mean Internet transmission. Those additional
paths were not exhaustively audited.

## Embedded speech and AI resources

**Speech synthesis is present.** The packaged Cyberon CReader SDK exports TTS
entry points and loads voice, prosody, language and text-normalization resources
from `usr/local/lib/tts_3rd/cyberon/data`. HTS/HMM model identifiers and model
loading code identify statistical speech-synthesis resources. REA traces UTF-8
text into synthesis and generated audio into a playback buffer. These generate
speech; they are not evidence of recognizing microphone input. Their normal
user-facing activation on this soundbar was not established.

**A working voice-trigger implementation was not found.** In the packaged
3,588-byte `libcast_assistant_1.0.so`, `GetVoiceTriggerDevice` and seven other
platform getters alias the same function returning null. The supported
device-action function clears its output vector. The Cast `process.json`
documents `enable-voice-trigger` as default false and tells partners to keep it
false unless the product publicly supports voice trigger. Actual runtime
command-line overrides were not observed.

`cast_shell` includes Chromium speech-recognition interfaces and references to
`SODAFiles/libsoda.so` and `SODALanguagePacks`. Those shared-framework references
do not establish a working recognizer. The 175-entry Cast ZIP contains neither
of those named payloads. A byte-signature scan of its 162 regular files found
none of the selected TensorFlow Lite, ONNX Runtime, GGUF, PocketSphinx,
DeepSpeech or Vosk markers. A REA search of Ghidra-defined `bdpprog` strings
also found no selected recognition-framework or hotword markers.

These are bounded searches: custom, stripped, compressed, remotely downloaded
or otherwise unrecognized models remain possible. Generic Assistant APIs in
shared libraries and the filename `Cortana-sweep.wav` do not establish an active
Assistant or Cortana implementation. `libnerualx.so` exposes DTS audio
postprocessing interfaces; its name alone does not establish machine learning.

## Artifact identity

The main update and `bdpprog` match the pin in the
[rear-speaker investigation](bar91-rear-bluetooth.md). The older, different
`firmware-workspace/analysis/rootfs` was excluded. The Cast executables are
inside `rootfs/res/ota.zip`; the empty `rootfs/chrome` directory is not evidence
that Cast is absent.

| Artifact | SHA-256 |
| --- | --- |
| `JBL_BAR_9_1.bin` | `028e5ea80971bfe6019efa3415fb5aeeb46a025589e0dd0f6560f09ecc6de892` |
| `bdpprog` | `86ec83472a10dd4278285123f8ecbfb96bdd97444dd98c3a56d5d87441c90b63` |
| `libmetrics.so` | `8c91bc8a8a03dce7d32b95d299b97f7a1324598b6481ee45c8d040de8f511038` |
| `libhdsp.so` | `1dcb71b2cd28df3fd8d36a16cd8cf71e47344ecb063cfab9efc55fcd94104258` |
| `res/ota.zip` | `74339ad299b6fad56b57d10e94f1d4ce4b376256ec1be8d2d65e15adc27552c6` |
| Cast `crash_uploader` | `392ba5e85946b27e56d8933fa9a0e584b2b41a8c907744de8de34faabacac537` |
| Cast `libcast_assistant_1.0.so` | `283835f3d40cac792a313dee63c3674362b27af2a37cccd179e46d240b814cb2` |

## Evidence ledger

Full vendor bytes, decompilations and REA bundles remain Git-ignored beneath
`research/private/telemetry-mic-audit/`. Addresses below are REA/Ghidra virtual
addresses for the exact artifact; shared-object imports use Ghidra's image
base and must not be substituted for file offsets.

| Conclusion | Native address | REA Evidence ID |
| --- | --- | --- |
| Metrics enabled constant | `libmetrics.so` `0x11af0` | `ev_fe798aca9a5a35561cbdf9e5a46aa06b91c2eb27c10d4e04ae0d67e81db3b479` |
| Local metrics writer | `libmetrics.so` `0x11af4` | `ev_3c3a1c6c487d544fd707c17a342aba4bfc3a090f8eda8edf21faf65372606fc6` |
| Wi-Fi roam | `net_mgr` `0x1c890` | `ev_6f9e9c14cee49c730a19a575a90244ccb76d60b0fa6859762a4b54088a99218a` |
| AP property storage | `net_mgr` `0x1aed0` | `ev_d286e7f2a0b381d3a40ba4110958edec4db5b2a68497a5aae2f405e6320056e5` |
| Diagnostic report creation | `process_monitor` `0x13534` | `ev_fe72ce585240019c7825e88a45e78effceefa2fbf93842759cf12bc881762f7f` |
| Crash consent and report fields | `crash_uploader` `0x2754c` | `ev_13614422ee39da9b7a178954c9591ea1c8c5779dc11ae935ae648540398a20c6` |
| Crash default endpoint | `crash_uploader` `0x27250` | `ev_f45411e63bcf5b3de30b29a0b9cc2448f04c13a5d9691bfde9a8151a7a254481` |
| Vendor property getter stub | `bdpprog` `0x731e08` | `ev_f58d9d2a05ec42890bc17aad704c8697c903c717296fdd5bfc0e99791a3b76a7` |
| Calibration processing | `libhdsp.so` `0x19e70` | `ev_a7605ffcf8c5ed6c585ed465bc978722eb669230abf0eba1c49655e636610ffc` |
| First microphone dump | `libhdsp.so` `0x39da0` | `ev_bcffeced5105e76717950350c36efd4e2500a4129630f542b70eaf503bc55766` |
| Second microphone dump | `libhdsp.so` `0x3ba2c` | `ev_f7c2f7682a9de5f7647acd7db38c368e3f5de52f385a9424606b1a3c08aa8969` |
| Calibration parameter bridge | `libHPP.so` `0x1127c` | `ev_c730a081eefc63edd5d0320f0784e4acea1a1c5d421c24177123a146bc823b88` |
| Speech synthesis | `libttspb.so` | `ev_aae27bba9dd21cebd0ec9d9d100d6a784ed6bc3308b94e910f1454749068ff89` |
| Assistant getter stub | Cast assistant shim `0x10794` | `ev_78823f7c5294f1f7ed2598f326212581c847e842174d27b194272fa587ce07f9` |

Remaining questions require runtime evidence: the effective consent preference,
which metrics are actually transmitted, generated dump contents, whether
calibration recordings persist, and whether any process reads or transmits
them. No packet capture was made or inspected in this audit.
