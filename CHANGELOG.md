# Changelog

User-facing changes go here. Before publishing another release, change the root
`VERSION` file and add a matching `## <version>` section. Release notes are
generated from that section plus the installation and recovery instructions.

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
