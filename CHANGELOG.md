# Changelog

User-facing changes go here. Before publishing another release, change the root
`VERSION` file and add a matching `## <version>` section. Release notes are
generated from that section plus the installation and recovery instructions.

## Unreleased

- Add **Cancel saved EQ**: while an EQ saved in standby, Smart Mode or on another audio route waits to be applied, the Save EQ button turns into Cancel saved EQ and goes back to the previous setting (new action `bar91.front-eq.cancel`).
- Fix Save EQ in standby, Smart Mode or on another audio route while an earlier EQ is still applied, for example after turning the bar off from the remote on a route where its EQ could not be removed. It failed with "Custom EQ is still active on this input … try again"; the new curve is now kept, the earlier EQ is removed once Standard audio plays, and the new one is applied.
- Explain how to get out of `PACKAGE_VERSION_CONFLICT_USE_ORIGINAL_USB` without the original USB or recovery ZIP: **Install / update everything** adopts a newer package and keeps settings, and **Uninstall** or a `RECOVER` marker works with any package. Setup now recommends both when it reads that status.
- Start SSH at boot even when the web remote fails to start, so the bar stays reachable for repair; the boot status still reports the remote failure.
- Start the web remote when `remote.pid` is stale after a reboot or power loss (the number now belongs to another process, or the file is empty) instead of failing the boot.
- Explain the boot integrity check in the USB guide: hand-edited toolkit files stop the next boot (`BOOT_RUNTIME_INTEGRITY_FAILED`), and `action=configure` repairs it.
- Keep SSH reachable when the soundbar changes its network address after startup, such as releasing Wi-Fi for Ethernet during boot or when a cable is connected later.
- Run common shell commands such as `cat`, `dd`, `awk`, `date` and `clear` in SSH sessions through BusyBox instead of crashing with `Segmentation fault`, while the bar's own utilities keep precedence; `reboot`, `poweroff` and `halt` stay with the firmware.
- Add a section menu to the web remote: a column beside the panels on wide screens and a row of chips under the header on phones, with running timers, messages on the bar and an applied Front EQ marked beside their sections. On phones, Connected shows only when the connection is not live and Refresh moves to the foot of the page.

## 0.3.0-dev.12

- Fix Windows USB preparation for already initialized disks and show detailed failures from elevated formatting.
- Select an active Ethernet or Wi-Fi connection for SSH access and `jbl.local` discovery, skipping disconnected interfaces with stale addresses.
- Guide USB setup using returned installation reports and recommend remote admin access for future network updates.
- **Breaking change:** replace recognized older standalone installations through an explicit clean reinstall with new settings, credentials and host identity. In-place migration is disabled; retain the original recovery files.
- Let display messages wait until the soundbar is on, with their duration starting when the bar wakes.
- Add controls to forget individual recent timers and messages, and correct the Front EQ status text.

## 0.3.0-dev.11

- Add experimental front-display messages with repeat, duration and until-cleared options; multiple messages take turns and can be cleared with the JBL remote.
- Add experimental timers with front-display countdowns, optional Chromecast chimes, adjustable ringing volume and duration, and controls to add time or stop an alarm. Restore standby after a timer wakes the bar to play its chime.
- Add an optional front-display clock for standby or whenever the bar is on, with a saved time zone; messages, timers and the bar's own screens take priority.
- Show center-level changes on the front display, reflect standby in the remote's power button, and add a confirmed soundbar restart action to the remote and API.
- Automatically clear Front EQ when it cannot remain active, show whether it is active or waiting, and simplify turning it off with Original.
- Migrate the public v0.1.0 revision 5 Smart Mode installation through USB setup. Explain installation failures, report Wi-Fi failure reasons and stop before installation if an old SSH server still owns port 2222.
- Recognize FAT32 drives as named by macOS 26, wait for the bar to turn on when AutoScript runs at boot, and remove staged Wi-Fi credentials after a failed install.
- Improve compact remote and setup layouts, with Timers and Messages side by side on wide screens.

## 0.3.0-dev.10

- Credit verified, unchanged SSH support files in update storage checks while retaining the full 2 MiB safety reserve.
- Improve remote typography and contrast, shorten help text, and sharpen the speaker illustration, with scaling on wide displays and wrapping on compact screens ([#30](https://github.com/kisinga/jbl-bar-toolkit/issues/30)).
- Add Network update (SSH) to repository and standalone setup: discover jbl.local or enter a private IP, authenticate with your SSH password, and update a managed bar without USB while retaining settings and keys.
- Run repository setup directly with Go; local runs and CI share the builder. Downloaded platform executables use verified published files without developer tools.
- Fix large-FAT32 readback and storage estimates; eject verified USBs automatically and include detailed setup failures in returned reports.
- Add a portable USB setup app for Windows, macOS and Linux: select a removable drive, optionally format FAT32/MBR, configure features and verify written files without installing software.
- Download verified toolkit and recovery files, read returned soundbar reports, and update the setup executable itself. Offline platform bundles include all toolkit files.
- Include verified project-owned Front EQ and Bluetooth helpers in default Go packages; retain native route and library checks.
- Add host-tested front-display notification contracts and explicitly report the native integration as unavailable pending qualification.
- Add a soundbar favicon, section tints and clearer selected/status colors to the remote and setup interface; respect reduced motion for setup progress.
- Reorganize the remote around the speaker illustration, nearby battery readings, visible sources and two sound columns. Audio sync, bass offset and System monitor start expanded; refresh GitHub screenshots.

## 0.3.0-dev.9

- Use one **Install / update everything** USB action for clean installation, recognized standalone migration and existing toolkit updates.
- Toolkit updates preserve installed feature/network settings, SSH credentials and host keys, and saved sound settings while replacing the bundled application and scripts.
- Check package integrity, installed runtime ownership, firmware, storage and the startup hook before updating. Unknown or modified installations stop with a status report.

## 0.3.0-dev.8

- Install from one USB package with an offline setup page and automatic hardware identification. Bar 9.1 is the only included model.
- Use the browser remote for volume, sources, playback and sound settings without a cloud account.
- Choose bass, center, rear speaker and Atmos levels directly; the remote confirms each step with the bar.
- Save an experimental bass offset from -3 to +3 dB, in 0.5 dB steps, across all five bass levels. Supported Standard playback is required for application.
- Optionally remember the Smart/Standard mode chosen before standby and restore it after wake.
- Find audio delay, rear speaker calibration, subwoofer pairing under Advanced, plus a separate experimental Front EQ panel. Availability depends on the device and build.
- Download public USB and independent recovery packages with checksums and source-commit metadata. Maintainers choose when to publish a prerelease through GitHub Actions.

The combined public USB installation, restart and recovery flow still needs
hardware qualification. Keep an existing installation's original recovery files;
cross-version updates are not implemented.
