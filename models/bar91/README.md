# JBL Bar 9.1 toolkit

[Toolkit overview](../../README.md) · [Architecture](../../docs/ARCHITECTURE.md)

Bar 9.1 is the only included installation target. Use the [root setup guide](../../README.md#get-started)
for the portable executable and local Go workflow. It configures the browser
remote, automatic Standard Mode, SSH and optional Wi-Fi from one USB.

The combined install/restart/recovery flow still needs physical qualification.
Preserve existing recovery files. Install/update recognizes managed toolkits and
the reviewed standalone chain; unknown installations are refused.

For an existing toolkit with SSH enabled, the setup tool’s **Network update (SSH)**
uses `jbl.local` or a private IP and your SSH password. It preserves installed settings
and keys without a USB. Use **USB setup** for initial installation, settings changes,
legacy migration or recovery. See the [setup and update guide](../../README.md#update-later).

[Configuration and installation reference](../../docs/models/bar91/USB-TOOLKIT.md) ·
[Standalone and historical Smart Mode releases](../../docs/models/bar91/STANDALONE.md)

## Compatibility

Support is restricted to the recorded **Bar 9.1** executable identities. The recorded firmware label is **23.06.41.80**; a matching version label alone is insufficient.

| Executable | Required SHA-256 |
| --- | --- |
| `bdpprog` | `86ec83472a10dd4278285123f8ecbfb96bdd97444dd98c3a56d5d87441c90b63` |
| `cli` | `8910fb852d2f994a519615fede404fbb036e3a8f5264cc69cdff57038af3c78f` |

The toolkit also checks persistent writable storage, free space and, when selected, the native SSH tools. Unsupported builds fail before installation. Keep these checks intact. Firmware updates can remove the installed services or invalidate compatibility.

[Bar 800/1000/1300 research](../../research/bar-family/README.md) records historical firmware comparisons, community protocol references and offline native replay. It does not establish compatibility with these installers.

## Recovery

For the **toolkit**, set `action=uninstall` or create an empty `JBLToolkit/RECOVER` file on the installation drive. Alternatively, extract the matching recovery ZIP to a clean FAT32 drive. Insert into the powered-on bar, wait **two minutes**, and require `RECOVERED_REBOOT_REQUIRED` in `JBLToolkit/STATUS.txt`. Remove USB and restart.

Recovery disables the managed services and restores the exact original startup hook when recognized. Files, credentials and Wi-Fi settings are retained; existing SSH sessions can remain until reboot. An externally changed startup hook is preserved and reported for manual review. [Full recovery instructions](../../docs/models/bar91/USB-TOOLKIT.md#recovery).

For **standalone installations**, use their [matching recovery instructions](../../docs/models/bar91/STANDALONE.md#remove-or-recover). Removing USB alone never uninstalls a persistent service.

## Troubleshooting

| Result | Next step |
| --- | --- |
| No new `JBLToolkit/setup.log` | Check FAT32, the first USB partition, USB root layout and the bar being fully on. |
| `CONFIG_INVALID` | Read the configuration error in `setup.log`; use the supplied keys and exact `true`/`false` values. |
| `PREFLIGHT_FAILED_CHECK_FIRMWARE_STORAGE_AND_NATIVE_SSH` | Check the supported firmware, writable storage/free space and native SSH requirements. |
| `EXISTING_COMPONENT_REMOVE_WITH_ORIGINAL_INSTALLER` | Follow the standalone recovery/migration instructions; preserve existing files. |
| `PACKAGE_VERSION_CONFLICT_USE_ORIGINAL_USB` | Choose Install / update everything for a newer build; use configure on the same package for deliberate credential/settings changes. |
| `TOOLKIT_DISABLED_USE_CONFIGURE_OR_RECOVER` | An earlier attempt failed. Retry with configure or recover on the same package; see [If an installation fails](../../docs/models/bar91/USB-TOOLKIT.md#if-an-installation-fails). |
| `BOOTSTRAP_FILE_MISSING_OR_UNSAFE` | A required file such as `recover.sh` (shown as "recover" in Windows) was removed from `JBLToolkit`. Restore your copy of the original USB. |
| `BUSY_RETRY_AFTER_BOOT_OR_CURRENT_OPERATION` | Let boot/current work finish, then reinsert. |
| Other failure or incomplete recovery | Read `setup.log`, retain the drive and use the [detailed troubleshooting guide](../../docs/models/bar91/USB-TOOLKIT.md#troubleshooting). |

## Validation and development

The toolkit has host fixtures for configuration validation, ARM packaging, install/reconfigure/recovery, startup-hook conflicts and partial failures. These do not establish device safety or audible behavior. Individual Go wake tests measured **7.77–7.82 seconds** from first observed awake state to confirmed Standard Mode on the test bar. Clean combined installs, recovery, mains cold boot and controlled CEC wakes remain to be qualified.

[Build and test](../../docs/models/bar91/DEVELOPMENT.md) · [Architecture and qualification](../../docs/models/bar91/USB-TOOLKIT.md#architecture) · [Device validation records](../../research/README.md) · [Report a result](https://github.com/kisinga/jbl-bar-toolkit/issues)

## Credits

The USB entry point and persistent-storage foundation build on [touch MAX's article](https://touch-max.ru/vzlom/zapusk-ssh-na-proshivke-jbl-9-1) and [almirus/jbl9.1](https://github.com/almirus/jbl9.1/tree/master/AutoScript). [JBL documents the manual Smart Mode sequence](https://support.jbl.com/howto/jbl-bar-9-1-lacking-dialogue-too-much-surround-us/000017755.html). [k1rnt's CLI](https://github.com/k1rnt/jbl-soundbar-cli/tree/7d10bdb1ebe0c5a77c9a4c14ebe4580bd3735309) supplied control/discovery inspiration for a different JBL transport. See [full credits](../../docs/models/bar91/STANDALONE.md#credits) and [community sharing notes](../../COMMUNITY.md).

Independent project; not affiliated with JBL. MIT covers project code and documentation. Vendor firmware and proprietary binaries are not bundled.
