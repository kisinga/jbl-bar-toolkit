# Bar 9.1 installation reference

Use the [root README](../../../README.md) for the setup workflow. Install root SSH, the browser remote and automatic Standard Mode together, or select only the features you need. One manager owns the startup hook and recovery path.

**Development build: host-tested; complete hardware installation and recovery are not yet qualified.** Existing per-feature device records do not validate this combined installer. No combined public release has been published.

## Prepare a USB

Use the [root setup guide](../../../README.md#get-started) for downloads and
configuration. For local development, install Go 1.25.7+ and run from the root:

```sh
go run ./apps/usbsetup --repo .
```

Setup uses current source in repository mode and verified public packages in a
downloaded executable. It checks write/readback integrity and safely ejects the
USB on success. Formatting requires explicit erase confirmation and an OS admin
prompt. Never run setup from the USB you intend to format. Keep the matching
recovery ZIP. Device-side firmware qualification still runs on the soundbar.

This reference covers installation safeguards, reports, configuration and recovery.
Optional helper development is documented in [development](DEVELOPMENT.md).

## Check, install, and verify

1. Keep the matching recovery ZIP. Use setup to prepare an empty writable FAT32
   first partition on a DOS/MBR USB, or explicitly choose formatting. Setup
   verifies the package, configuration and stored bytes before activating
   AutoScript last. After successful eject, unplug the drive from your computer.

2. Set `JBLToolkit/config.txt` to `action=check`. Turn the bar fully on, let playback settle, insert USB and wait **five minutes**. Return USB to the computer. Read `CHECKS.txt`, `PREFLIGHT.txt`, `DIAGNOSTICS.txt`, `native-check.json`, `STATUS.txt` and `setup.log`. Require `CHECK_PASSED_READY_FOR_INSTALL_OR_CONFIGURE`. This operation reads native state and makes temporary write/copy/rename checks; it does not install, change networking, stop services or press controls. Existing standalone stores/hooks are reported as blockers and preserved.
3. With a passing check and a clean installation target, set `action=install` on the same USB. Reinsert with the bar fully on and leave controls alone for **five minutes**. Automatic Standard selection may briefly mute audio. Return USB and require `INSTALLED_PENDING_REBOOT_TEST`. Read `RESULT.txt`, `setup.log` and exported logs. A partial failure is not installation success.
4. With SSH selected, inspect `logs/connection.txt` and `host_ed25519.pub`. Compare the host-key fingerprint with the SSH client's prompt before accepting it. Test `ssh -p 2222 root@<bar-ip-address>`, an interactive shell and SFTP. With the web remote selected, open `http://jbl.local` or the bar's local IP and configured port. Use the IP if mDNS is unavailable.
5. Remove USB, disconnect mains for **60 seconds**, reconnect, let LOAD finish, turn on, and wait for services/automation. Verify the chosen services return without USB and check normal audio, volume, mute, inputs and standby/CEC wake.
6. Set `action=verify` and reinsert. Require `VERIFIED_CURRENT_BOOT_PENDING_USER_TESTS` and inspect `VERIFY.txt`. Verification requires an intact installed runtime/configuration, matching package/hook, selected service process/readiness, readable native state, and a **different kernel boot ID** from installation/configuration with a successful managed boot receipt. When awake with automation selected, it checks Standard Mode. It does not reconfigure services.
7. Exercise recovery and a subsequent USB-free restart using the instructions below. Retain the reports plus observations for audio, SSH/SFTP, standby/CEC and recovery. Only completed hardware evidence can change the qualification record from pending.

The native probe runs the packaged ARM executable from a temporary private directory, covering a noexec USB mount. It checks pinned firmware/CLI identities, native memory layouts/state and libraries needed by bundled optional helpers. It reports registered kernel filesystems for the exFAT investigation; it never runs control actions or helpers. `kernel_filesystems` is a snapshot, not proof of USB automount or AutoScript support.

The third device check passed root, BusyBox, writable VFAT, persistent UBIFS,
firmware, SSH tools, boot ID, atomic no-replace publication and native reads.
Its space failure exposed an overly large two-copy estimate. The current budget
uses one new persistent payload copy plus 2 MiB reserve; probe executables run
in RAM. A byte-identical managed remote is reused during configuration, so it
does not need another full binary allocation. The measured case (6,340 KiB payload,
13,224 KiB available) now fits an 8,388 KiB requirement. This correction has host
fixtures; the new complete migration/installation still needs its device run.

Receipts and readiness checks cannot prove audible results, successful authentication, physical USB removal, recovery after a subsequent reboot or every future wake. These remain explicit manual qualification items. The check action runs a prerequisite matrix using the same guards as installation: root, BusyBox, USB mount, persistent UBIFS, pinned firmware/CLI, selected SSH tools, free space, boot ID, atomic storage/publication and native state/helpers. `PREFLIGHT.txt` records every PASS, FAIL or SKIP. Atomic writes are skipped unless root, BusyBox and both storage guards pass. Native inspection can run independently of other failed guards; `NATIVE_RESULT` records its outcome. Use the current report rather than an older snapshot. `DIAGNOSTICS.txt` includes BusyBox identity/applets/option help, mounts, registered filesystems, hook hash, store ownership markers and selected script/snapshot hashes for migration review, without exporting component credentials/configuration.

### Filesystem choice

Use **FAT32** for the canonical process. The test drive was prepared with a 512 MiB FAT32 first partition and the rest unallocated; that size is a conservative test choice, not a demonstrated maximum. The installer is small and does not need exFAT's large-file support.

exFAT is **not qualified**. The second hardware check found no exFAT entry in the live `/proc/filesystems`; it reported `msdos` and `vfat`, and the actual USB mount was writable VFAT. This is a bounded live-kernel observation, not an exFAT mount test. The [Bar 9.1 owner manual](https://th.jbl.com/on/demandware.static/-/Sites-masterCatalog_Harman/default/dw9f4087d6/pdfs/JBL_SB_Bar%209.1_3D_SURROUND_Owner%27s%20Manual_EN.pdf) describes USB service/update use but does not establish exFAT support. The inspected 4.9.134 firmware module tree contains `fat.ko`, `vfat.ko` and `fuse.ko`, with no named exFAT driver found. This does not rule out built-in or userspace support. Qualification would require a separate exFAT automount → AutoScript → writable report → recovery test on the bar. Host exFAT support alone is insufficient; the current installer deliberately rejects non-VFAT device mounts.

## Configuration reference

`config.txt` is plain text with `key=value` lines. Blank lines, leading `#` comments and Windows line endings are accepted. The seven settings below are required; the three credential fields are optional. Unknown or duplicate keys, invalid types and shell expressions are rejected. Keep the file under 8 KiB.

| Key | Values | Behavior |
| --- | --- | --- |
| `format` | `1` | Configuration schema version. |
| `action` | `check`, `update`, `install`, `migrate`, `verify`, `configure`, `uninstall` | Qualification preflight; automatic install/update/migration; explicit install, migration, verification, configuration or recovery. |
| `ssh` | `true`, `false` | Starts/stops the managed SSH listener and its future startup. Port is fixed at `2222`. Existing sessions can remain until reboot. |
| `web_remote` | `true`, `false` | Enables/disables HTTP and discovery. |
| `auto_standard` | `true`, `false` | Automatically selects Standard Mode after wake. Independent of HTTP/SSH. |
| `smart_mode_memory` | Optional `true`, `false` (default `false`) | With `auto_standard=true`: after wake, restores the mode chosen before standby instead of always selecting Standard. Rejected without `auto_standard=true`. |
| `allowed_cidr` | Canonical private IPv4 CIDR, `/16` through `/30` | Shared trusted LAN for SSH/web. Network address required, e.g. `192.168.1.0/24`, not `192.168.1.20/24`. |
| `web_port` | `80`, or `1024`–`65535` except `2222` | HTTP port. Ports 80/8080 retain the remote's mutual fallback; other ports are used alone. |
| `ssh_password_hash` | Optional SHA-512 crypt `$6$…` value | Owner credential, generated by the portable setup app. Overrides a separately bundled hash on installation/configuration. |
| `wifi_ssid_hex` | Optional lowercase UTF-8 hex, 1–32 decoded bytes | Provision this Wi-Fi name. Requires `wifi_psk_hex`. |
| `wifi_psk_hex` | Optional 64 lowercase hex characters | Derived WPA2 key, generated by the portable setup app. Requires `wifi_ssid_hex`. |

Enabling automatic Standard Mode uses the existing guarded button gesture, not a permanent firmware setting. Disabling automation leaves the current sound mode alone; it does not actively turn Smart Mode on. The existing browser button selects Standard Mode manually. A direct, symmetric Smart Mode on/off switch is not implemented.

With `smart_mode_memory=true`, the bar comes back in the mode it was left in. The firmware itself always returns to Smart after power-on, so only the existing Standard gesture is needed: it runs after a wake when Standard was the last choice, and nothing is sent when Smart was. The choice is recorded only after the wake is handled, so the bar's own Smart right after wake is never taken for a choice, and a failed wake records nothing. A mode selected from the page or Home Assistant is recorded after five seconds; any other change (physical remote, firmware) only after it has lasted one minute without a standby, because the firmware switches to Smart about 25 seconds before it reports standby after the power button. It is kept in `/var/JBLRemote/smart-mode.json` across standby, mains loss and service restarts. Without a record, Standard is assumed, as with plain `auto_standard`. A mode selected from the page or Home Assistant before the automation acts wins for that wake and is remembered.

Useful combinations:

| SSH | Web | Auto Standard | Result |
| --- | --- | --- | --- |
| true | true | true | Full toolkit |
| false | true | true | Browser controls and wake automation |
| false | false | true | Automation only, with no HTTP listener or discovery |
| true | false | false | SSH only |
| false | true | false | Browser controls with manual mode selection |
| false | false | false | All toolkit services disabled; manager/recovery retained |

Credentials may live in `config.txt`, or be bundled by an advanced private build.
The browser page converts passwords to the accepted derived forms locally; do
not paste plaintext passwords into shell scripts. SSH needs either a valid
config hash or a bundled hash. Wi-Fi works independently of SSH and uses the
native supplicant/network manager. Omit both Wi-Fi fields to preserve the current
connection. The toolkit targets `wlan0`; custom interfaces/names, SSH ports and
public-key login are not exposed. EQ persistence and sound-at-wake presets are
not configurable promises.

## Change features or collect logs

Reinsert the original installation USB with `action=install` to export status. Expect `ALREADY_INSTALLED_LOGS_EXPORTED`; configuration edits are not applied and services are not restarted. Routine exports deliberately omit config.txt because it can contain credentials.

To make changes, edit `JBLToolkit/config.txt`, set `action=configure`, and repeat installation verification. This restarts managed services while retaining SSH host keys. Enabling SSH needs credentials in config or the original immutable bundle. An edited config hash applies to the stopped managed listener; open sessions can remain until reboot. Native startup guards still apply. Return to `action=install` after successful configuration for passive log collection.

## Update everything from USB

An existing toolkit with SSH enabled can also use **Network update (SSH)** in the
same setup tool. See the [owner workflow](../../../README.md#update-later). It uses
the same manager and native/storage safeguards from private RAM staging, requires
a checked managed installation and retains settings and SSH keys. No USB mount is
required. Physical network-update/reconnect qualification is pending; retain USB
recovery. Initial installation, legacy migration and settings changes still use USB.

Use the latest USB package and choose **Install / update everything** in
the portable setup app (`action=update`). Insert it into the powered-on bar and wait five
minutes. Read `STATUS.txt`, remove the USB, restart and verify as above.

The installer detects a clean bar, a recognized standalone migration source,
or a managed toolkit. Existing toolkit updates preserve installed feature/network
settings, SSH credentials and host keys, and sound-state files such as the saved
bass offset. All bundled application/manager/SSH scripts are updated. JBL firmware
and vendor libraries remain on the device. Same-package reinsertion exports logs.
Use `action=configure` separately for deliberate settings changes.

Before replacing an existing toolkit, the updater checks installed runtime
integrity, ownership, the startup hook and normal firmware/storage prerequisites.
It commits the new package identity after service readiness. Failures after
replacement starts disable services and attempt recovery; this is not an atomic
rollback to the previous app. Keep the matching recovery package and inspect the
status before rebooting. Modified/unknown installations are preserved and refused.
The new path is host-tested. One recognized standalone migration reached
`INSTALLED_PENDING_REBOOT_TEST`, and the owner confirmed the updated web interface
worked before reboot ([device record](../../../models/bar91/validation/usb-update-device.json)).
Cold boot, managed-to-managed updates and recovery qualification remain pending.

## Recovery

Choose one:

- Set `action=uninstall` in the original USB's config.
- Create an empty `JBLToolkit/RECOVER` file, with no `.txt` extension. This takes precedence over config parsing and application-payload checks.
- Extract the matching recovery ZIP to a clean FAT32 USB drive.

Insert into the powered-on bar and wait **two minutes**. Require `RECOVERED_REBOOT_REQUIRED` in `JBLToolkit/STATUS.txt`. Remove USB and restart. If the bar is unresponsive, reconnect mains with recovery USB inserted, let LOAD finish and inspect the result. Repeated recovery is safe for a recognized installation.

Recovery first disables future toolkit startup and asks owned services to stop. It does not force-kill a sound gesture. It restores the exact pre-toolkit startup-hook bytes only if the current hook matches the recorded managed hook; if originally absent, it removes the managed hook. External hook changes remain intact and produce `RECOVERY_INCOMPLETE_READ_LOG`. The toolkit disable marker remains so the preserved hook cannot launch its dispatcher.

Installed files, credentials, native Wi-Fi profiles and existing SSH sessions are retained. Reboot completes session shutdown. Recovery is deactivation, not secure deletion or a factory reset. To resume later, remove the USB's `RECOVER` marker and use `action=configure` with the same original package, provided the original/managed hook is still recognized.

## Existing standalone installations

Normal `install` refuses existing `/var/JBLStandard`, `/var/JBLLocalSSH` or
`/var/JBLRemote` stores and recognizable leftover startup commands.

**Install / update everything** automatically migrates one reviewed chain
(explicit `action=migrate` is also available): rev6 Smart Mode or the public `v0.1.0` (rev5) release,
the known prototype/portable SSH boot script and the recorded standalone remote.
`check` writes `MIGRATION_CHECK.txt`. Even an eligible legacy chain remains a
blocker for the advanced first-install-only action; the automatic update action
selects the reviewed migration path. Before changes,
migration checks exact owners, scripts, hook snapshots, native prerequisites
and no-replace publication. It stops only recognized services, lets gesture/boot
workers finish, then moves the old stores into `/var/JBLToolkitLegacy` on the
same filesystem without copying their payloads. The original SSH host key is
retained. A new manager owns the new installation and uses Go wake automation.

The archive is inactive and retained on the bar. Recovery restores the clean
pre-toolkit baseline; it **does not reactivate the legacy chain**. A failed or
interrupted migration retains its archive and receipt for review; an owned,
recognized partial retirement can resume. Unknown scripts, symlinks, changed
hooks and unrelated live PIDs stop migration. Keep the original installers for
their own recovery; do not remove unknown stores to force eligibility.

The public `v0.1.0` Smart Mode release installs rev5 (`JBL_STANDARD_HOOK_REV5`), so it is
not a migration source. `MIGRATION_CHECK.txt` then reports
`LEGACY_OWNERSHIP_OR_FILE_TYPE_MISMATCH`, even when the SSH and web remote files match.
Remove that chain in the standalone order below.

For other chains, use the original [standalone removal order](STANDALONE.md#remove-or-recover): web remote → SSH → Smart Mode. Standalone removers can retain files. Preserve those directories outside active paths and verify the original hook is restored before installation. This remains developer-controlled work requiring access and recovery.

**Restart the bar after the last remover and before installing.** A standalone SSH
server keeps listening on port 2222 until the bar restarts, even after its files are
removed. The new SSH step then fails with `SSH_INSTALL_FAILED`, and `sshd.log` shows
`Bind to port 2222 ... failed: Address already in use`.

## If an installation fails

A failed installation leaves the toolkit disabled on purpose, so a half-configured
setup can never start at the next boot. Getting back on track depends on keeping the
exact files you started with.

**Before you insert the USB**

- Copy everything on the prepared USB, including the whole `JBLToolkit` folder, to a
  folder on your computer. Keep the recovery ZIP next to it. Configure and recovery
  need this exact package; a newly prepared USB is a different package and is
  refused with `PACKAGE_VERSION_CONFLICT_USE_ORIGINAL_USB`.
- Don't delete or rename files in `JBLToolkit`. Windows hides file extensions by
  default, so `recover.sh` shows up as **recover**. It is a required script, not the
  `RECOVER` marker described under [Recovery](#recovery). Without it the USB stops at
  `BOOTSTRAP_FILE_MISSING_OR_UNSAFE`. To see extensions, turn on **File name
  extensions** in File Explorer's **View** menu.

**After a failure**

1. Read `JBLToolkit/STATUS.txt` and the newest `RESULT=... EXIT=...` line in
   `JBLToolkit/setup.log`. Look the code up in [Troubleshooting](#troubleshooting).
2. Fix the cause. For example, restart the bar if an earlier SSH server still held
   port 2222, or correct the Wi-Fi network for `WIFI_SETUP_INCOMPLETE`.
3. Retry with the same package. If the USB was changed, restore the copy from your
   computer first. Then use `action=configure` (in the setup app: **Apply
   configuration changes**, without formatting) and insert it. Inserting it again with
   `action=install` only reports `TOOLKIT_DISABLED_USE_CONFIGURE_OR_RECOVER`.
4. To back out instead, follow [Recovery](#recovery) with the same USB or the matching
   recovery ZIP.

| You see | What it means |
| --- | --- |
| `TOOLKIT_DISABLED_USE_CONFIGURE_OR_RECOVER` | An earlier attempt failed and left the toolkit disabled. Use configure or recovery with the same package. |
| `PACKAGE_VERSION_CONFLICT_USE_ORIGINAL_USB` | The USB holds a different package from the one on the bar. Restore your copy of the original USB. |
| `BOOTSTRAP_FILE_MISSING_OR_UNSAFE` | `dispatch.sh`, `launch.sh`, `common.sh`, `recover.sh` or `BOOTSTRAP.SHA256SUMS` is missing from `JBLToolkit`. Restore your copy of the original USB. |

If the original package is lost, run recovery from the matching recovery ZIP and ask
in an [issue](https://github.com/kisinga/jbl-bar-toolkit/issues) before trying again.
Don't remove `/var/JBLToolkit` by hand to force a fresh install.

Toolkit-managed components have `TOOLKIT` markers. Their standalone web installers and SSH hook tools refuse to modify them; use the toolkit configuration/recovery instead.

## Troubleshooting

Read `JBLToolkit/STATUS.txt` and the newest `RESULT=... EXIT=...` entry in `setup.log`. Log files append across insertions. `RESULT.txt` is only rewritten on successful configuration; after a failure, an older result is not current evidence.

| Status | Meaning/action |
| --- | --- |
| `CHECK_BLOCKED_READ_CHECKS` | Read current `CHECKS.txt` for the preflight reason and existing-store/hook blockers. No installation was attempted. |
| `VERIFICATION_INCOMPLETE_READ_VERIFY` | Read `VERIFY.txt`; a missing new boot, damaged runtime or unavailable service prevents verification. Manual items remain required even after machine checks pass. |
| `PACKAGE_INTEGRITY_FAILED` | Immutable scripts/binary/credentials differ from the package manifest. Re-extract the original package. Config changes do not require checksum edits. Emergency recovery still works. |
| `CONFIG_INVALID` | The parser explains the error in `setup.log`. Fix data; never insert shell code or edit the generated plan. |
| `PREFLIGHT_FAILED_CHECK_FIRMWARE_STORAGE_AND_NATIVE_SSH` | No installation changes: read `PREFLIGHT_REASON` in `setup.log` for the failed firmware/CLI, UBIFS, BusyBox, free-space or native SSH guard. Preserve checks. |
| `PREFLIGHT_REASON=SSH_PORT_2222_IN_USE_RESTART_BAR_FIRST` | Another process listens on port 2222, usually the SSH server of a standalone installation you removed or moved aside. It keeps running until the bar restarts. Restart the bar, then insert the same USB again. No installation changes were made. |
| `SSH_CREDENTIALS_NOT_BUNDLED` | Add your derived SSH credential through the setup app/config, or use the original bundled private hash. There is no shared/default password. |
| `MIGRATION_SSH_CREDENTIALS_REQUIRED_NO_CHANGES` | Migration with SSH selected needs your config/bundled hash before any legacy service is disabled. |
| `TOOLKIT_DISABLED_USE_CONFIGURE_OR_RECOVER` | A prior failure/recovery disabled startup. Read logs, then explicitly configure or recover. Passive reinsertion does not clear the guard. |
| `REMOTE_NOT_READY` / `REMOTE_EXITED_DURING_STARTUP` | Read `setup.log` and retained `/var/JBLRemote/remote.log`. Startup is disabled on failure. |
| `SSH_INSTALL_FAILED` | Inspect `setup.log` and retained SSH staging logs; do not treat a listening socket as a successful login. Wi-Fi changes may already have occurred. |
| `WIFI_SETUP_INCOMPLETE` | The bar did not join the configured network within 130 seconds. `setup.log` and `JBLToolkit/WIFI_STATUS.txt` name the reason: `WIFI_NOT_CONNECTED` usually means a wrong passphrase, a 5 GHz-only or hidden network, or a network out of range; `INVALID_SSID`/`INVALID_PSK` mean the prepared values were rejected; other codes point to the bar's Wi-Fi service. `WIFI_EXIT=143` means the step timed out. Correct the network in the setup app and choose **Apply configuration changes** (`action=configure`) for the same USB, without formatting. |
| `STARTUP_HOOK_CHANGED_PRESERVED` / `HOOK_CHANGED_BEFORE_COMMIT` | Another installer/manual change modified the hook. Preserve it; review before retrying. |
| `RECOVERY_INCOMPLETE_READ_LOG` | A process did not stop or hook ownership did not match. Future toolkit startup is disabled; retain files and review logs, then reboot if needed. |

If no entry log appears, check the USB root, FAT32 and first partition. The soundbar must finish booting and be fully on for the normal install. A global operation lock prevents installation/configuration from overlapping boot's bounded SSH wait. Let it finish before reinsertion. A hard power interruption can leave a stale `/tmp` lock until reboot. Some bars run AutoScript only while booting, never on insertion: unplug mains, insert the USB, reconnect, and turn the bar on within about three minutes. Check, install and configure wait that long for the bar to report On.

Logs can contain SSIDs, addresses, host keys and device information. Review before sharing. Password hashes and derived Wi-Fi keys are never included in the toolkit's routine `logs/` export. Keep full private packages and captures out of issues.

## Architecture

The FAT32 AutoScript launches `JBLToolkit/dispatch.sh` in the background. It verifies the bootstrap, selects the exact executable profile (or an owned installation for recovery), and records `HARDWARE.txt` before calling the model launcher `JBLToolkit/launch.sh`. It closes inherited vendor sockets before taking a global operation lock. A strict BusyBox AWK parser converts whitelisted configuration tokens into a generated plan in that private lock directory; raw configuration is never executed.

Immutable scripts, the ARM remote and private credential payloads have a checksum manifest. Editable `config.txt` is outside that manifest and is validated on the host and again on the device. Checksums detect accidental alteration; they are not signatures authenticating an untrusted USB source.

Before changes, preflight checks the writable first USB VFAT mount at `/mnt/sda1` from `/dev/sda1` or JBL’s `/dev/block/sda1` alias, running native firmware, CLI identity, persistent writable UBIFS `/var`, free space, BusyBox operations, atomic storage I/O and native no-replace store publication, native execution/state and optional SSH tools/helper libraries. A new `/var/JBLToolkit` owns the original-hook snapshot, exact package identity, validated configuration and one dispatcher. An explicit disable marker covers partial work. The supported firmware BusyBox lacks `mv -T`; the packaged Go binary uses Linux `renameat2(RENAME_NOREPLACE)` instead. Preflight tests that operation on persistent storage, including refusal to replace an existing directory. Unsupported kernels/filesystems fail closed. The installed runtime/configuration has a separate integrity manifest checked at every managed boot. The complete managed hook is committed only after service-start checks. Installation/configuration records the kernel boot ID; boot records its own ID and marks success only after selected services pass readiness checks. Failures attempt bounded deactivation/recovery and retain evidence. Native Wi-Fi profile changes are retained rather than automatically reversed.

SSH reuses its existing guarded installer with a private input directory and an explicit `--managed` mode that skips its separate startup hook. The Go remote runs with HTTP or in automation-only mode. It opens no HTTP listeners or discovery registrations in automation-only mode. The toolkit does not install the legacy shell Smart Mode watcher, so there is one automation engine. Shutdown permits the existing gesture to release keys and restore console state.

Recovery uses only the USB recovery scripts and recorded ownership/snapshots. It does not depend on an intact installed manager, valid configuration, running SSH or a working remote executable. Unknown hooks/processes are preserved. One shared boot/installation lock prevents configuration while the SSH boot worker is still waiting for networking.

Host checks cover the real ARM build/package, credential handling, the actual config parser, managed startup/readiness using a host executable, safe reinsertion, explicit configuration, failure recovery and exact hook restoration. Native firmware, SSH authentication and Wi-Fi are fixture boundaries. Before publishing a combined release, qualify on a clean supported bar: all feature combinations, Wi-Fi persistence, USB-free mains boot, standby/CEC wake, interrupted installation, changed-hook recovery, SSH/SFTP, normal controls and rollback. Keep the established `v0.1.0` Smart Mode release unchanged.
