# Bar 9.1 custom front-display messages

Investigated 5 November 2026. **A native path for caller-supplied text exists in
the supported firmware. An external toolkit command and hardware qualification
are still needed.** No device commands, firmware patches or installer changes
were made during this investigation.

## Target and existing support

This analysis applies to ARM32 `bdpprog`, firmware label `23.06.41.80`, SHA-256
`86ec83472a10dd4278285123f8ecbfb96bdd97444dd98c3a56d5d87441c90b63`.
Addresses below are process virtual addresses for that exact executable.

The remote already reads the display manager's state in
[`apps/remote/setup.go`](../apps/remote/setup.go). The manager uses state at
`0xc65494` and context at `0xd8a088`. Those existing reads establish useful
integration points; they do not implement text output.

## Preferred candidate: a temporary native message

The internal function at **`0x626d70`**, identified by its own diagnostic as
`Tonly_vfdmg_set_command`, accepts a command number and payload pointer. For
command **`0x5b`**, the complete path is:

1. Copy up to 32 bytes of caller text into a zeroed, 72-byte message containing
   type `1`, command `0x5b`, and the payload at offset `8`.
2. Submit it through `0x626b38`, which calls `c_app_send_msg` using the display
   task handle at `0xd8a0ac`.
3. The native consumer at `0x624314` sets temporary-message selector
   `0xc65494 + 0x1c` to `1` (the native version-text slot), records the current
   tick at `0xd8a114`, and copies the 32-byte text to `0xd8a120`.
4. Renderer `0x6224c0`, case `1`, converts that caller text for display. It
   expires through the normal message manager and restores native display
   selection.

The timeout helper at `0x6217f0` uses text length times 550 ms, substituting a
length of nine for nonempty text shorter than six characters. Thus `DONE` is
approximately 4.95 seconds and `INSTALLING` approximately 5.5 seconds. These
are static timing calculations, not measured display durations. Higher-priority
native messages can delay or prevent visibility.

Conceptual **in-process** call, not a shell command or an external linking API:

```c
/* Only inside the matching bdpprog process, after the display task is ready. */
Tonly_vfdmg_set_command(0x5b, "SSH READY");
```

Use at most **31 printable ASCII bytes plus NUL**. The native sender and
consumer copy fixed-width fields; they do not provide a general safe string
API. Prefer short messages such as `INSTALLING`, `SSH READY`, `DONE`, `FAILED`
and `REBOOT`. Font coverage and actual scrolling still need device testing.

This would reuse the native version-text slot. Overlap with an actual version
request must be handled by the eventual adapter. The sender returns zero even
when the manager is uninitialized or the queue operation fails, so a successful
function return cannot establish that a message appeared.

## Other paths and limitations

| Path | Static finding | Practical implication |
| --- | --- | --- |
| `app.vfdmg.b disp_msg` | Broker at `0x626fee` calls `0x7458e4` with fixed `ABCDEFGH`. | No arbitrary-text argument in this handler. |
| `app.vfdmg.b scroll_msg start` | Calls `0x745920` with fixed `ABCDEFGHIJK`. | This is a demo, not a custom-message command. |
| Static text at `0x7458e4` | Physical-VFD branch copies at most 63 bytes and sends IOM command `0x20006`. | Native primitive exists, but bypasses normal manager selection. |
| Scrolling text at `0x745920` | Physical-VFD branch sends IOM command `0x2000a`, with a field set to 300. | Arbitrary text is supported by this primitive; speed units and hardware behavior remain unqualified. |
| Native command `0xb3` | JBL's upgrade animation calls `0x626d70(0xb3, "{WAIT")` and other spinner strings. Consumer copies 32 bytes to `0xd8a0f4` and selects priority-level-3 state `0x25`. | Strong evidence of native dynamic text. This path has no timeout in its renderer and is unsuitable for routine notifications without a proven cleanup path. |
| Exported `a_vfdmg_set_command` at `0x626ee0` | Inspected body filters commands, copies a payload locally, then returns without submitting it. | The exported symbol is not a usable replacement for the internal Tonly sender on this build. |

The generic physical-VFD primitives use process-local IOM handles and resource
manager calls. Finding their addresses does not make them callable from a
separate Go or shell process. The inspected broker command also dereferences
arguments without validating their count; do not probe it with missing args.

An [earlier Bar 9.1 SSH article](https://touch-max.ru/vzlom/zapusk-ssh-na-proshivke-jbl-9-1)
quotes a commented `CLI_app.vfdmg.b scroll_msg start` example borrowed from
older MediaTek player work. That example does not establish custom-text support
or a display test on this firmware. The handler analysis above is the relevant
evidence for the pinned build.

## Memory-write route (qualified)

Added 8 November 2026. Instead of calling the sender inside `bdpprog`, the
remote performs the consumer's own writes through `/proc/<pid>/mem`, the same
way it already reaches the bass and console fields. No code runs in the native
process.

- **Writes.** Case `0x5b` of the consumer (`0x625b86`) selects level-2 slot `1`
  at `0xc65494+0x1c`, stores the start tick at `0xd8a114` and copies 32 bytes
  of text to `0xd8a120`. The remote writes the text, then the tick, then the
  selector, in that order.
- **Tick.** `0x253434` returns `clock_gettime(CLOCK_MONOTONIC)` divided by the
  period at `0xcf1854`, which `0x2534b8` initializes to
  `1000 / sysconf(_SC_CLK_TCK)` (10 ms).
- **Expiry.** The renderer `0x62260e` compares the elapsed ticks, unsigned,
  with `length × 550 ms` and then writes `117`.
- **Standby.** The priority function `0x624190` draws level 3 first. In
  standby, level 3 holds `28`; its case at `0x621d6c` draws a blank screen, and
  the module sets it about 1.7 s after `STDBY` (`0x621d48`). All 24 literal
  references to `0xc65494` are inside the display module.
- **On the bar** (owner's Bar 9.1, firmware 23.06.41.80):
  - the tick matched within one second of a native VOL message;
  - `HELLO FABIEN` stayed 6.75 s (6.6 s computed) and `LE FRIGO EST OUVERT`
    10.58 s (10.45 s computed); the bar then showed `HDMI IN`;
  - lower case is not displayed as lower case;
  - in standby, with level 3 lifted to `52` for the message and set back to
    `28`, the text was visible and the screen went black again; the TV, the
    HDMI source and Bluetooth were not woken;
  - repeats, blanks, waiting behind native messages, replacement by id and
    durations were then exercised with the shell prototype.

[`docs/DISPLAY_MESSAGES.md`](../docs/DISPLAY_MESSAGES.md) describes the
resulting `bar91.display-message` extension. The installer-feedback adapter
below can reuse this route; it is still not wired into the installer.

## Integration work

Follow the [implementation guide](../docs/models/bar91/DISPLAY-FEEDBACK.md) for
the native-entry gate, adapter/client contract, event hooks and qualification
sequence. Its proposed commands and adapter are not implemented yet.

The next useful proof is a single temporary `0x5b` message on the owned bar,
followed by observation of native timeout, source/volume feedback and unchanged
sound settings. It requires a reviewed way to invoke the native sender within
`bdpprog`. This investigation has not established a stock external CLI command
or cross-process RPC that accepts this payload.

A toolkit implementation should expose a bounded operation such as
`display_message("SSH READY")` through an adapter that executes in the native
process/task context. The adapter's loading and registration mechanism remains
to be designed and validated. It should:

- Verify executable identity and display readiness, serialize with existing
  controls, and decline during calibration, pairing or firmware upgrade.
- Validate and copy text before queuing it; use the native temporary-message
  path and its timeout, with no persistent display-state changes.
- Confirm queue consumption and distinguish that from observed screen output.
- Make feedback best effort: display failure must not abort installation or
  change an operation's result.

After qualification, installer stages in `models/bar91/usb/manage.sh` and
completion/error paths in `launch.sh` could call that adapter. `STATUS.txt`,
logs and reboot qualification remain authoritative. An install completion
message must not claim that the later reboot test has already passed.

## External-entry audit

A second REA/Ghidra pass on the pinned executable tested the likely entry routes:

| Candidate | Observed boundary |
| --- | --- |
| Display task queue | `c_app_send_msg` packs the payload into `x_msg_q_send`. Its queue is a `calloc`-allocated ring with process-local pthread synchronization, not an externally addressable OS message queue. |
| Dynamic loading | Resolved `dlopen` callers are DLL utility `0x2fce90` and audio initialization `0x511d88`. The former's only analyzed caller loads fixed TTS library `libttspb.so` in slot 6; the latter loads fixed vendor decoder paths. No caller-selected external module loader was established. |
| CLI registration | `x_cli_attach_cmd_tbl` stores native table pointers. The inspected callers supply static vendor tables; this does not register code from another process. |
| `callapi` | Table entry `0xc2fa88` points to Thumb handler `0x23b611`. Its 22 bytes make four fixed direct calls and return zero, without parsing arguments or dispatching a supplied address. |
| Media RPC registration | Five resolved registration functions expose media, sound, configuration and upgrade operations. None registers the display sender. `x_MtkPb_Ctrl_Reg_CLI` records three callback identifiers; its handler does not load a module or call the display sender. |

These are bounded static findings, not proof that every indirect route is absent.
The native-entry gate remains open. The next proof must identify a reachable
command/loader that accepts project-owned code or the bounded text payload;
exported registration functions alone do not supply that mechanism. No live
device attachment, process restart, injection or display command was performed.
The second pass is retained under `research/private/display-access/hooks/`.

## Evidence and coverage

Raw vendor code and full evidence remain Git-ignored in
`research/private/display-access/`. REA/Ghidra 12.1.2 provided the following
shipped-artifact evidence:

| Finding | Evidence ID |
| --- | --- |
| Internal Tonly sender and payload widths | `ev_50ae9be213f13a1d17d996e672c6d262761a6f764c6eef22ea215f035a801546` |
| Native display queue submission | `ev_81a339695a1bc159b73908e16e4ce38b5125afebcf7478b1b7e13af20188e25d` |
| Temporary text rendering and expiration | `ev_21e034ec09e40dd7d1bb684f17306586b5da27e83580b0f248e0a52f5c91d26a` |
| Timeout helper instructions | `ev_7c2264cb5493cfb373cf732953e1f4c88209dfe587551ffa7a6c474e54587b9f` |
| JBL's caller-supplied upgrade spinner text | `ev_661a7dc15c55f321b796c484041e1872c27c33a32e6e940b204c949f844bfd2d` |
| Static text primitive | `ev_56b6911937caa9dd9a3714a787c12b550a5a2c916367cf875f622397cd6e1ca2` |
| Scrolling primitive | `ev_6a5ec26bd05f6e2d7102d7cf65574839418c463ecfbaca16e50bae914bec8b78` |
| Exported wrapper's actual body | `ev_d04718fc9a26d18d3d3af9a7fa99d2d13405b6d4eac3eecaf24bb192b9380c6c` |
| Process-local queue implementation | `ev_ceee31541781cdb9498cf0c6a29be1b3c3bf8ab8ac260a7d589f3683726b7c87` |
| Resolved dynamic-loader callers | `ev_f20b050e67d006d2ee4c34de78dde6ee8be26a3b31690457383cbfd40ef19f94` |
| Fixed TTS loader caller | `ev_380f58369774c0fe694bcc75d24b12192972708ac52c75dc21b7a6c2b1b6bc5d` |
| Native CLI table registration | `ev_fccdc2eaf10ef18598aff6be944a9ea9543391f724229b898aa35d4dd3cd6801` |
| `callapi` handler bytes | `ev_304479bad9c135fb5682885de0fd553250dde1bd514e1789e87406d402b65877` |
| Media RPC CLI registration handler | `ev_145ea048775e73d7e2ca1edb2073308a518131507db8f30d50a210b2f0ffe9ca` |

Default Ghidra analysis did not define functions at the broker and native task
callback. Initial numeric lookups fell back to unrelated file-offset mappings;
those results are excluded from the conclusions. Focused offline Ghidra
disassembly/decompilation at the explicit Thumb entries `0x626fee` and
`0x624314`, together with ELF-mapped Capstone disassembly and literal-table
reads, supplied the missing handler evidence. The focused consumer output has
unrecovered jump tables; conclusions here use its explicit message-command
cases and independently identified renderer, not an assertion of complete
consumer control flow. Scripts, logs, output and the REA evidence index are
retained in the private evidence directory.

**Answered:** native variable-text support and a temporary-message candidate.
**Partially answered:** external integration route.
**Resolved later (memory-write route above):** live screen behavior, standby,
interruption and restoration, on the owner's bar.
**Unresolved:** full glyph coverage (punctuation), and the installer-feedback
integration (`internal/display` contract, installer stages).
