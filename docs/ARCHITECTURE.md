# Architecture and source ownership

The repository is a JBL Bar toolkit with model-qualified implementations. Bar 9.1
is the current runtime target. The source reorganization separates ownership
while retaining its package format, runtime behavior and recovery contracts.

## Product goal and delivery order

An owner should be able to install from one USB, open a usable web remote and
enable optional features. The setup tool accepts either USB or a network update
over SSH using the same model update transaction. Model support can grow
from read-only inspection to network controls and then on-bar installation;
those capabilities do not have to arrive together.

The current priorities are to finish Bar 9.1 combined-install/recovery hardware
qualification and network-update qualification, while using the original Bar 1300
as the first real extensibility test. A network remote hosted on another computer
can deliver useful Bar 1300 controls before an installation route exists. Keep
the product entry points simple and use real responses to drive any further
abstractions. Host fixtures do not substitute for physical qualification.

## Boundaries

```text
apps/usbsetup/                   portable computer-side executable (root Go module)
internal/usbsetup/               USB, mDNS/SSH updates, verification and self-update
apps/remote/                     Go application, native control code and browser UI
  internal/server/               shared HTTP/security/state delivery
  internal/extension/            typed feature registry and joined workers
  internal/preview/              simulated controls or read-only fixture replay
  internal/jblone/               bounded read-only protocol inspection
  cmd/inspect/                   collect private responses and a preview fixture
  www/components/                scoped UI components, no build step
  backend_bar91.go                native composition and v1 compatibility
  lifecycle_bar91.go              native workers and restoration
jbl                              contributor prepare / models / usb / remote commands
packaging/                       embedded setup interface and release notes
  usb/                          generated hardware dispatcher template
models/
  catalog.json                   model discovery and honest support status
  README.md                      implementation versus research status
  bar91/
    profile.json                 reviewed hashes, architecture and qualification
    README.md                    Bar 9.1 user entry point
    usb/                         combined installation/configuration/recovery manager
    ssh/                         native SSH namespace, credentials and lifecycle
    remote/                      device lifecycle scripts and project C helpers
    smart-mode/                  pinned standalone Smart Mode source
    packaging/                   original standalone USB instructions/recovery text
    validation/                  public Bar 9.1 device and historical host evidence
docs/models/bar91/               USB, standalone, troubleshooting and development guides
tools/                          stable host command entry points
tests/
  check_cli.py                   current local API/CLI contract
  check_profiles.py              firmware eligibility consistency/refusal
  bar91/                         model host fixtures and pinned payload baselines
research/                       reviewed findings and reproducible research tools
research/private/               ignored vendor artifacts, captures and raw analysis
```

The device app remains one module; setup/build tools use a separate root module. `internal/server.Backend[S]` supplies snapshots,
commands and model/feature metadata without importing native code. The HTTP/SSE
implementation is shared with the loopback preview. Snapshot types retain the
existing v1 JSON shape and implement pure publication/comparison methods.

The native device and feature files remain in the application package to preserve
contributor workflows. `backend_bar91.go` composes them behind the server
contract; `lifecycle_bar91.go` joins feature/native workers before restoration.
A future family backend can use the server and UI without importing that code.
See [the component guide](COMPONENTS.md) for interfaces and a runnable example.

Bar 1300 is the first planned hardware consumer of those shared boundaries.
One JBL One-family package now collects read-only responses and conservative
fixtures; a live backend can grow there after readback is qualified. Its small
computer-hosted collector reuses the existing preview/UI for fixtures.
Compatible variants are device/capability data;
separate code requires an observed behavioral difference. The
[extensibility check](COMPONENTS.md#the-first-real-extensibility-check) identifies
the remaining Bar 9.1 UI assumptions and the expected scope of that change.

## Firmware eligibility

`models/bar91/profile.json` is reviewed build metadata. `tools/repo_layout.py`
locates sources and loads that exact profile. Optional helper builders use its
library identities and the remote builder uses its ARM target settings.

`tools/check_profiles.py` generates the remote's embedded executable/library
identities and checks disagreement with existing shell guards. Remote builds fail
on stale generated identities. No configuration file on USB or device selects a
different firmware profile or overrides native memory guards.

The profile describes eligibility; it cannot establish that offsets, key events,
DSP routing, standby behavior or recovery work on a new build. Those remain
model-specific code and qualification obligations.

## Installation and runtime ownership

The shared USB dispatcher checks its minimal bootstrap and selects an included
implementation by exact executable identity, recording `HARDWARE.txt`. Emergency
recovery can instead select an already owned store, without depending on working
firmware or the app. Currently only Bar 9.1 is packaged; its known AutoScript
startup mechanism is not assumed to exist on newer bars. `tools/hardware.py`
supplies the reviewed release matrix independently of the research catalog.

The combined 9.1 USB coordinator is canonical for new installations and owns the managed startup hook. It selects SSH,
browser controls and automatic Standard Mode, preserves the original hook and
provides independent recovery. Its implementation is under `models/bar91/usb/`;
the [USB guide](models/bar91/USB-TOOLKIT.md) defines status, configuration,
migration and recovery behavior.

`tools/prepare_usb.py` validates a private package and a mounted removable USB's
DOS/MBR, first partition and FAT32 identity. It flushes and reads back the payload
before writing AutoScript last. The on-device `check` action probes native
execution/state, selected helper libraries, atomic storage operations and existing
installation blockers. Check mode records every independent prerequisite and skips storage writes when prerequisite guards fail; installation still stops at the first failure. The firmware uses `/dev/block/sda1` as a first-partition device alias. Installation records a runtime/configuration manifest and
kernel boot ID. The boot dispatcher verifies installed integrity and records
readiness; `verify` requires a subsequent kernel boot and current service/native
checks. Audio, authentication, physical USB removal, standby and recovery remain
hardware qualification obligations, recorded separately from machine receipts.

Standalone installers refuse toolkit-owned stores. Normal installation preserves
and refuses existing standalone paths. Explicit `action=migrate` accepts only
the exact reviewed rev5 (public v0.1.0) or rev6/SSH/remote scripts, owners and hook snapshots. It retires
their live paths into a private archive on the same filesystem, retaining the
SSH host key, before normal installation. Unknown chains remain blocked.
Recovery disables the toolkit; it does not silently reactivate archived services.

Repository locations and artifact/device paths are separate contracts. Builders
still emit `AutoScript/AutoScript`, `JBLToolkit/`, `JBLLocalSSH/`, and the existing
standalone layouts. Runtime `/var/JBLToolkit`, `/var/JBLLocalSSH`,
`/var/JBLRemote`, `/var/JBLStandard`, markers, ports and rollback logic retain
their existing meanings. Published release filenames and GitHub links retain
their original names.

## Shared core and model features

`internal/extension` supplies a small generic registry. Model composition
registers a descriptor, typed action handlers and optional projections of an
already captured snapshot. Front EQ and center level are the first Bar 9.1
modules. Their optional workers are registered alongside actions and joined on
shutdown; model policy still owns hardware sequencing and cleanup. Actions such as `bar91.front-eq.apply` share the core HTTP guard,
serialization lock and timeout. Legacy browser action names remain aliases.
`GET /api/features` advertises build inclusion and omission reasons, and the
model's owner's manual links (`manuals`, by language, https only);
`state.extensions` carries optional feature state. A bundled feature still needs
valid current firmware and state. Extension state projections perform no I/O. The browser uses known local
component IDs to bind namespaced state/actions; metadata cannot load scripts.
Legacy API fields/actions remain compatible. Components own their own drafts
and history while the shell owns one connection and one action queue.

Modules compile into the one static `CGO_ENABLED=0` ARM executable. Go's native
[plugin documentation](https://pkg.go.dev/plugin) describes toolchain/dependency
compatibility and race-detection limitations and suggests static composition or
IPC as alternatives. Static composition fits USB deployment without a second
loader, dependency manager or dynamic plugin ABI. Add a separate process only
when a concrete feature needs isolation; no plugin marketplace is needed here.

| Concern | Share | Keep qualified separately |
| --- | --- | --- |
| User experience | Setup flow, web assets, CLI, HTTP protections, state delivery | Capability visibility and model-specific feature labels/ranges |
| Runtime | State/action contracts and typed extension registry | 9.1 native memory/CLI versus newer-family WAMP/HTTP backend |
| Bar 1000 and 1300 | Family protocol codecs/transports after semantics are verified | Model identity, speaker topology, DSP/ALSA bundles, tuning and feature evidence |
| Installation | ZIP/manifest building, setup page, checksums, CI | Entry point, persistent storage, startup hook, process identities and recovery |
| Research | Extraction/replay tools and source attribution | Exact artifact hashes, traces and hardware qualification |

The original 800/1000/1300 research shows common setup-wamp/audio-ui/dsp-client
components. A future family backend can share a client where commands and state
actually agree. It must not inherit the 9.1 `/var` persistence or AutoScript
assumptions: the researched family uses `/data` persistence and `/var` in RAM,
and no installation/persistence route has been qualified. Keep their current
catalog entries as research instead of adding empty runtime adapters.

A capability needs semantics: explicit setter versus toggle; state readback;
source restrictions; EQ processing scope and ranges; persistence; and verification
status. Retain the current serialized controls, fresh-state checks and recovery
handling. An ambiguous toggle timeout must not trigger a blind retry.

The newer-bar research establishes that Smart Mode, a surround mixer enum and
wireless rear connection state are distinct paths. Its model-specific EQ bundles
and `/data` persistence also differ from 9.1. Those findings guide a separate
implementation; they do not justify passing another model through this installer.

## Release boundary

The portable setup executable downloads a USB ZIP with a prebuilt remote and
independent recovery. Platform bundles include those ZIPs for offline use. Public assets start check-only and
contain no owner credentials. Owners can put derived SSH/Wi-Fi credentials into
the local config, which remains outside the immutable payload manifest and is
strictly validated on both host and bar. Routine logs do not export that config.
CI uploads only explicit distribution assets and creates draft prereleases for
version tags. [Release guidance](RELEASING.md) keeps host success distinct from
device qualification and preserves existing standalone release contracts.

## Migration verification

The reorganization started from the existing single-USB working files. Its path
migration preserved shell payloads and native helper sources byte for byte.
Subsequent USB qualification checks extend that coordinator in its model folder. The pinned
standalone builder validates its original baselines, and its ZIP output is
compared with the build made before the move. Shared profile generation changes
where Go hash definitions live while preserving their values.

Host tests, ARM package builds, Go race/vet checks and browser fixtures exercise
the updated paths. These checks retain their original limits: they do not qualify
installation, playback or recovery on a physical device.

## Incremental delivery and current limits

`./jbl dev` and `./jbl dev --fixture <normalized.json>` exercise the same shared
server and embedded UI. Fixture replay is read-only and does not qualify model
control or installation. `./jbl test` is the fast no-device/no-BusyBox path;
`./jbl test --full` runs host integration, race checks and vet. Required suite
reports remain checked without freezing the number of regression cases.

The merged center-wake and EQ/power fixes (#7/#8), center labels (#9) and advanced
controls (#5) are incorporated through upstream `3a52f58`, retaining their tests.
The shared server accepts the existing `value` request field and native-defined
bounded action budgets; it does not know native action names. Native files keep their
existing structure; further path moves should be isolated from behavior changes.

Bootstrap, release application and owner configuration are distinct responsibilities.
USB and SSH use the same guarded Bar 9.1 manager. SSH authenticates against a
confirmed public host key, checks the installed runtime, and transfers verified
files to a private RAM directory. Only managed-installation updates can use this
entry; installation, migration and recovery use USB. Network updates retain the verified SSH listener while updating its support files.
The detached worker survives connection loss and reports its result for host readback. Timeouts preserve evidence
and report an uncertain outcome, never success. Neither route qualifies hardware boot
and recovery. New model work can progress through normalized replay and verified
network control independently of a jailbreak/bootstrap implementation.
