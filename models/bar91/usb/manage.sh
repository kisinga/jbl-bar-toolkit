#!/bin/sh
USB=${JBL_TOOLKIT_USB:-/mnt/sda1/JBLToolkit}
. "$USB/common.sh" || exit 1
. "$USB/preflight.sh" || exit 1
umask 077
mutated=0
stage=
update_existing=false
keep_ssh=false
config_source=$USB/config.txt
finish() {
    trap '' HUP INT TERM
    [ -z "$stage" ] || [ ! -d "$stage" ] || "$BB" rm -rf "$stage"
    if [ "$2" != 0 ] && [ "$mutated" = 1 ]; then
        # Leave the installation disabled on failure, retaining recovery evidence.
        /bin/sh "$USB/recover.sh" || :
    fi
    printf '%s\n' "$1" > "$USB/STATUS.txt"
    printf 'RESULT=%s EXIT=%s\n' "$1" "$2"
    "$BB" sync
    exit "$2"
}
trap 'finish INTERRUPTED_RECOVER_OR_CONFIGURE 1' HUP INT TERM
export_logs() {
    [ ! -e "$USB/logs" ] && [ ! -L "$USB/logs" ] || directory "$USB/logs" || return 1
    "$BB" mkdir -p "$USB/logs" || return 1
    for name in boot.log runtime.manifest install.boot_id boot.id BOOT_STATUS.txt; do
        [ ! -f "$STORE/$name" ] || atomic_copy "$STORE/$name" "$USB/logs/$name" || return 1
    done
    for name in connection.txt host_ed25519.pub; do
        [ ! -f "$SSH_STORE/$name" ] || atomic_copy "$SSH_STORE/$name" "$USB/logs/$name" || return 1
    done
    [ ! -f "$SSH_STORE/BOOT_STATUS.txt" ] || atomic_copy "$SSH_STORE/BOOT_STATUS.txt" "$USB/logs/ssh-BOOT_STATUS.txt" || return 1
    [ ! -f "$REMOTE_STORE/remote.log" ] || atomic_copy "$REMOTE_STORE/remote.log" "$USB/logs/remote.log" || return 1
}
(cd "$USB" && "$BB" sha256sum -c SHA256SUMS) >/dev/null 2>&1 || finish PACKAGE_INTEGRITY_FAILED 1
"$BB" awk -f "$USB/parse-config.awk" "$USB/config.txt" > "$LOCK/plan" || finish CONFIG_INVALID 1
. "$LOCK/plan" || finish CONFIG_INVALID 1
"$BB" rm "$LOCK/plan"
case "$ACTION" in migrate) ACTION=install;; install|configure|update) ;; *) finish INVALID_INSTALL_ACTION 1;; esac
if [ -e "$STORE" ] || [ -L "$STORE" ]; then
    owned || finish TOOLKIT_STORE_CONFLICT 1
    [ -z "$("$BB" find "$STORE" -type l)" ] || finish TOOLKIT_SYMLINK_CONFLICT 1
    if [ "$ACTION" = update ]; then
        # Validate the current installation before stopping or replacing anything.
        runtime_integrity >/dev/null 2>&1 || finish UPDATE_RUNTIME_INTEGRITY_FAILED_NO_CHANGES 1
        if "$BB" cmp -s "$STORE/package.manifest" "$USB/SHA256SUMS" && [ ! -e "$STORE/DISABLED" ]; then
            export_logs || finish LOG_EXPORT_FAILED 1
            finish ALREADY_INSTALLED_LOGS_EXPORTED 0
        fi
        regular "$STORE/config.txt" || finish UPDATE_CONFIG_MISSING_NO_CHANGES 1
        unset SSH_PASSWORD_HASH WIFI_SSID_HEX WIFI_PSK_HEX
        "$BB" awk -f "$USB/parse-config.awk" "$STORE/config.txt" > "$LOCK/plan" || finish UPDATE_CONFIG_INVALID_NO_CHANGES 1
        . "$LOCK/plan" || finish UPDATE_CONFIG_INVALID_NO_CHANGES 1
        "$BB" rm "$LOCK/plan"
        ACTION=update
        config_source=$STORE/config.txt
        update_existing=true
        if [ "$SSH" = true ] && [ -z "${SSH_PASSWORD_HASH-}" ]; then
            regular "$STORE/ssh-input/password.hash" || finish UPDATE_SSH_CREDENTIALS_MISSING_NO_CHANGES 1
            SSH_PASSWORD_HASH=$("$BB" cat "$STORE/ssh-input/password.hash") || finish UPDATE_SSH_CREDENTIALS_MISSING_NO_CHANGES 1
            { "$BB" cat "$STORE/config.txt"; printf '\nssh_password_hash=%s\n' "$SSH_PASSWORD_HASH"; } |
                "$BB" awk -f "$USB/parse-config.awk" >/dev/null || finish UPDATE_SSH_CREDENTIALS_INVALID_NO_CHANGES 1
        fi
        # Updating binaries must not reprovision the owner's network.
        unset WIFI_SSID_HEX WIFI_PSK_HEX
    else
        "$BB" cmp -s "$STORE/package.manifest" "$USB/SHA256SUMS" || finish PACKAGE_VERSION_CONFLICT_USE_ORIGINAL_USB 1
    fi
    if [ "$ACTION" = install ]; then
        export_logs || finish LOG_EXPORT_FAILED 1
        [ ! -e "$STORE/DISABLED" ] || finish TOOLKIT_DISABLED_USE_CONFIGURE_OR_RECOVER 1
        finish ALREADY_INSTALLED_LOGS_EXPORTED 0
    fi
    if ! "$BB" cmp -s "$HOOK" "$STORE/hook.managed" && ! "$BB" cmp -s "$HOOK" "$STORE/hook.before"; then
        [ ! -e "$HOOK" ] && [ ! -L "$HOOK" ] && regular "$STORE/hook.absent" || finish STARTUP_HOOK_CHANGED_PRESERVED 1
    fi
else
    [ "$ACTION" != update ] || ACTION=install
    [ "$ACTION" = install ] || finish NOT_INSTALLED_USE_INSTALL 1
    for path in "$SSH_STORE" "$REMOTE_STORE" /var/JBLStandard; do
        [ ! -e "$path" ] && [ ! -L "$path" ] || finish EXISTING_COMPONENT_REMOVE_WITH_ORIGINAL_INSTALLER 1
    done
    [ ! -e "$HOOK" ] && [ ! -L "$HOOK" ] || regular "$HOOK" || finish STARTUP_HOOK_UNSAFE 1
    if regular "$HOOK" && "$BB" grep -Eq 'JBL(Standard|LocalSSH|Remote|Toolkit)' "$HOOK"; then
        finish EXISTING_COMPONENT_HOOK_PRESERVED 1
    fi
fi
for spec in "$SSH_STORE:JBL_LOCAL_SSH_REV1" "$REMOTE_STORE:JBL_WEB_REMOTE_REV1"; do
    path=${spec%:*}; expected=${spec##*:}
    if [ -e "$path" ] || [ -L "$path" ]; then
        component_owned "$path" "$expected" || finish COMPONENT_STORE_CONFLICT 1
        regular "$path/TOOLKIT" && [ "$("$BB" cat "$path/TOOLKIT")" = "$OWNER" ] || finish COMPONENT_NOT_MANAGED 1
        [ -z "$("$BB" find "$path" -type l)" ] || finish COMPONENT_SYMLINK_CONFLICT 1
    fi
done
[ "$SSH" != true ] || [ -n "${SSH_PASSWORD_HASH-}" ] || regular "$USB/files/ssh/password.hash" || finish SSH_CREDENTIALS_NOT_BUNDLED 1
if ! preflight; then
    printf 'PREFLIGHT_REASON=%s\n' "${PREFLIGHT_REASON:-UNKNOWN}"
    finish PREFLIGHT_FAILED_CHECK_FIRMWARE_STORAGE_AND_NATIVE_SSH 1
fi
if [ "$update_existing:${JBL_TOOLKIT_TRANSPORT:-usb}:$SSH" = true:ssh:true ]; then
    # The native daemon is unchanged. Preserve the listener that carries this
    # update instead of trying to recreate its mount namespace from inside it.
    (JBL_SSH_STORE="$SSH_STORE"; export JBL_SSH_STORE;
     . "$USB/files/ssh/common.sh" && daemon_running) || finish SSH_LISTENER_CHANGED_NO_CHANGES 1
    keep_ssh=true
fi
if ! owned; then
    # A power loss cannot publish half an ownership/rollback record. Orphaned
    # stage directories have no hook and can never run at boot.
    stage=$("$BB" mktemp -d "$STORE.stage.XXXXXX") || finish STORE_CREATE_FAILED 1
    printf '%s\n' "$OWNER" > "$stage/OWNER.txt" || finish STORE_OWNER_WRITE_FAILED 1
    marker "$stage/DISABLED" || finish DISABLE_FAILED 1
    if regular "$HOOK"; then
        atomic_copy "$HOOK" "$stage/hook.before" || finish HOOK_BACKUP_FAILED 1
        "$BB" stat -c %a "$HOOK" > "$stage/hook.mode" || finish HOOK_BACKUP_FAILED 1
    else
        : > "$stage/hook.before"; : > "$stage/hook.absent"
    fi
    atomic_copy "$stage/hook.before" "$stage/hook.managed" || finish HOOK_STAGE_FAILED 1
    printf '\r\nCLI(CLI_exec sh %s/boot.sh &)\r\n' "$STORE" >> "$stage/hook.managed" || finish HOOK_STAGE_FAILED 1
    atomic_copy "$USB/SHA256SUMS" "$stage/package.manifest" || finish MANIFEST_SAVE_FAILED 1
    "$BB" sync
    publish_directory "$stage" "$STORE" || finish STORE_PUBLISH_FAILED 1
    [ ! -d "$stage" ] || finish STORE_APPEARED_PRESERVED 1
    stage=
fi
mutated=1
marker "$STORE/DISABLED" || finish DISABLE_FAILED 1
stop_remote || finish REMOTE_STOP_FAILED_REBOOT_AND_RECOVER 1
[ "$keep_ssh" = true ] || stop_ssh || finish SSH_STOP_FAILED_REBOOT_AND_RECOVER 1
for name in common.sh boot.sh parse-config.awk; do
    atomic_copy "$USB/$name" "$STORE/$name" || finish MANAGER_COPY_FAILED 1
done
atomic_copy "$config_source" "$STORE/config.txt" || finish CONFIG_SAVE_FAILED 1
[ ! -e "$STORE/ssh-input" ] || directory "$STORE/ssh-input" || finish SSH_STAGE_CONFLICT 1
"$BB" mkdir -p "$STORE/ssh-input" || finish SSH_STAGE_FAILED 1
# A failed Wi-Fi step leaves its credentials staged; never reprovision from them.
"$BB" rm -f "$STORE/ssh-input/wifi.ssid.hex" "$STORE/ssh-input/wifi.psk.hex" || finish WIFI_STAGE_CLEANUP_FAILED 1
for path in "$USB"/files/ssh/*; do
    if [ "$update_existing" = true ]; then
        case "${path##*/}" in password.hash|wifi.ssid.hex|wifi.psk.hex) continue;; esac
    fi
    atomic_copy "$path" "$STORE/ssh-input/${path##*/}" || finish SSH_STAGE_FAILED 1
done
if [ -n "${SSH_PASSWORD_HASH-}" ]; then
    printf '%s\n' "$SSH_PASSWORD_HASH" > "$STORE/ssh-input/password.hash" || finish SSH_CREDENTIALS_FAILED 1
fi
if [ -n "${WIFI_SSID_HEX-}" ]; then
    printf '%s\n' "$WIFI_SSID_HEX" > "$STORE/ssh-input/wifi.ssid.hex" || finish WIFI_CREDENTIALS_FAILED 1
    printf '%s\n' "$WIFI_PSK_HEX" > "$STORE/ssh-input/wifi.psk.hex" || finish WIFI_CREDENTIALS_FAILED 1
fi
printf 'ALLOWED_PC=%s\nINTERFACE=wlan0\nAUTOSTART=1\n' "$ALLOWED_CIDR" > "$STORE/ssh-input/settings.conf" || finish SSH_SETTINGS_FAILED 1
if regular "$STORE/ssh-input/wifi.ssid.hex"; then
    "$BB" mkdir -p "$STORE/network" || finish NETWORK_STORE_FAILED 1
    JBL_SSH_USB="$STORE/ssh-input" JBL_SSH_STORE="$STORE/network" "$BB" timeout 130 /bin/sh "$STORE/ssh-input/wifi.sh" || {
        wifi_exit=$?
        # wifi.sh writes its reason into the bar's staging directory, which the
        # owner cannot read without SSH. Copy that one status token to the USB.
        wifi_reason=UNKNOWN
        if regular "$STORE/ssh-input/WIFI_STATUS.txt"; then
            IFS= read -r wifi_reason < "$STORE/ssh-input/WIFI_STATUS.txt" || :
            case "$wifi_reason" in
                ''|*[!A-Z0-9_]*) wifi_reason=UNKNOWN;;
                *) printf '%s\n' "$wifi_reason" > "$USB/WIFI_STATUS.txt" || :;;
            esac
        fi
        printf 'WIFI_REASON=%s WIFI_EXIT=%s\n' "$wifi_reason" "$wifi_exit"
        finish WIFI_SETUP_INCOMPLETE 1
    }
    # Wi-Fi was handled independently; do not repeat provisioning in SSH setup.
    "$BB" rm "$STORE/ssh-input/wifi.ssid.hex" "$STORE/ssh-input/wifi.psk.hex" || finish WIFI_STAGE_CLEANUP_FAILED 1
fi
if [ "$SSH" = true ]; then
    if [ ! -e "$SSH_STORE" ]; then
        "$BB" mkdir -m 700 "$SSH_STORE" || finish SSH_STORE_CREATE_FAILED 1
        printf 'JBL_LOCAL_SSH_REV1\n' > "$SSH_STORE/OWNER.txt" || finish SSH_OWNER_WRITE_FAILED 1
    fi
    marker "$SSH_STORE/TOOLKIT" || finish SSH_OWNERSHIP_FAILED 1
    if legacy_archive_owned && regular "$LEGACY_STORE/RETIRED"; then
        component_owned "$LEGACY_STORE/JBLLocalSSH" JBL_LOCAL_SSH_REV1 || finish LEGACY_SSH_KEY_OWNER_CONFLICT 1
        for name in host_ed25519 host_ed25519.pub; do
            if [ -e "$SSH_STORE/$name" ] || [ -L "$SSH_STORE/$name" ]; then
                regular "$SSH_STORE/$name" && "$BB" cmp -s "$LEGACY_STORE/JBLLocalSSH/$name" "$SSH_STORE/$name" || finish LEGACY_SSH_KEY_CHANGED_PRESERVED 1
            else
                atomic_copy "$LEGACY_STORE/JBLLocalSSH/$name" "$SSH_STORE/$name" || finish LEGACY_SSH_KEY_COPY_FAILED 1
            fi
        done
    fi
    (cd "$STORE/ssh-input" && "$BB" sha256sum ./*.sh settings.conf password.hash > checksums.txt) || finish SSH_MANIFEST_FAILED 1
    ssh_mode=--managed
    [ "$keep_ssh" != true ] || ssh_mode=--managed-network-update
    "$BB" rm -f "$STORE/ssh-input/STATUS.txt" || finish SSH_STATUS_CLEAR_FAILED 1
    JBL_SSH_USB="$STORE/ssh-input" JBL_SSH_STORE="$SSH_STORE" /bin/sh "$STORE/ssh-input/start.sh" "$ssh_mode" || finish SSH_INSTALL_FAILED 1
    marker "$SSH_STORE/TOOLKIT" || finish SSH_OWNERSHIP_FAILED 1
    "$BB" rm -f "$SSH_STORE/BOOT_DISABLED" || finish SSH_ENABLE_FAILED 1
fi
if [ "$WEB_REMOTE" = true ] || [ "$AUTO_STANDARD" = true ]; then
    if [ ! -e "$REMOTE_STORE" ]; then
        "$BB" mkdir -m 700 "$REMOTE_STORE" || finish REMOTE_STORE_FAILED 1
        printf 'JBL_WEB_REMOTE_REV1\n' > "$REMOTE_STORE/OWNER.txt"
    fi
    marker "$REMOTE_STORE/TOOLKIT" || finish REMOTE_OWNERSHIP_FAILED 1
    atomic_copy "$USB/files/remote" "$REMOTE_STORE/remote" || finish REMOTE_COPY_FAILED 1
    "$BB" chmod 700 "$REMOTE_STORE/remote" || finish REMOTE_MODE_FAILED 1
    atomic_copy "$USB/files/remote-start.sh" "$REMOTE_STORE/start.sh" || finish REMOTE_COPY_FAILED 1
    headless=false
    [ "$WEB_REMOTE" = true ] || headless=true
    printf '{"listen":":%s","allowed_cidr":"%s","auto_standard":%s,"smart_mode_memory":%s,"headless":%s}\n' "$WEB_PORT" "$ALLOWED_CIDR" "$AUTO_STANDARD" "${SMART_MODE_MEMORY:-false}" "$headless" > "$REMOTE_STORE/config.json" || finish REMOTE_CONFIG_FAILED 1
    "$BB" rm -f "$REMOTE_STORE/DISABLED" "$REMOTE_STORE/READY" || finish REMOTE_ENABLE_FAILED 1
    /bin/sh "$REMOTE_STORE/start.sh" || finish REMOTE_START_FAILED 1
    count=0
    while [ "$count" -lt 15 ]; do
        if process_owned "$REMOTE_STORE/remote.pid" "$REMOTE_STORE/remote -config $REMOTE_STORE/config.json " && regular "$REMOTE_STORE/READY" && [ "$("$BB" cat "$REMOTE_STORE/READY")" = "$process_pid" ]; then
            break
        fi
        "$BB" sleep 1; count=$((count+1))
    done
    [ "$count" -lt 15 ] || finish REMOTE_NOT_READY 1
    "$BB" sleep 2
    process_owned "$REMOTE_STORE/remote.pid" "$REMOTE_STORE/remote -config $REMOTE_STORE/config.json " || finish REMOTE_EXITED_DURING_STARTUP 1
fi
(
    set -- "$STORE/common.sh" "$STORE/boot.sh" "$STORE/parse-config.awk" "$STORE/config.txt"
    if [ "$SSH" = true ]; then set -- "$@" "$SSH_STORE"/*.sh; fi
    if [ "$WEB_REMOTE" = true ] || [ "$AUTO_STANDARD" = true ]; then
        set -- "$@" "$REMOTE_STORE/remote" "$REMOTE_STORE/start.sh" "$REMOTE_STORE/config.json"
    fi
    "$BB" sha256sum "$@"
) > "$LOCK/runtime.manifest" || finish RUNTIME_MANIFEST_FAILED 1
atomic_copy "$LOCK/runtime.manifest" "$STORE/runtime.manifest" || finish RUNTIME_MANIFEST_FAILED 1
"$BB" rm "$LOCK/runtime.manifest"
boot_id > "$STORE/install.boot_id" || finish BOOT_RECEIPT_FAILED 1
printf 'INSTALL_PENDING_NEW_BOOT\n' > "$STORE/BOOT_STATUS.txt" || finish BOOT_RECEIPT_FAILED 1
if regular "$HOOK"; then
    "$BB" cmp -s "$HOOK" "$STORE/hook.managed" || "$BB" cmp -s "$HOOK" "$STORE/hook.before" || finish HOOK_CHANGED_BEFORE_COMMIT 1
    atomic_copy "$STORE/hook.managed" "$HOOK" || finish HOOK_COMMIT_FAILED 1
else
    [ ! -L "$HOOK" ] && regular "$STORE/hook.absent" || finish HOOK_CHANGED_BEFORE_COMMIT 1
    # Keep the expected snapshot on a different inode: editing the live hook
    # must never edit the ownership reference used by recovery.
    atomic_copy "$STORE/hook.managed" "$STORE/hook.activation" || finish HOOK_COMMIT_FAILED 1
    "$BB" ln -T "$STORE/hook.activation" "$HOOK" || finish HOOK_COMMIT_FAILED 1
    "$BB" rm "$STORE/hook.activation" || finish HOOK_COMMIT_FAILED 1
fi
# Commit the new package identity only after the runtime and hook are ready.
atomic_copy "$USB/SHA256SUMS" "$STORE/package.manifest" || finish MANIFEST_SAVE_FAILED 1
"$BB" sync
"$BB" rm "$STORE/DISABLED" || finish ENABLE_FAILED 1
{
    printf 'SSH=%s (port 2222)\nWEB_REMOTE=%s\nAUTO_STANDARD=%s\nSMART_MODE_MEMORY=%s\n' "$SSH" "$WEB_REMOTE" "$AUTO_STANDARD" "${SMART_MODE_MEMORY:-false}"
    [ "$WEB_REMOTE" != true ] || printf 'WEB=http://jbl.local:%s (or the bar IP and port)\n' "$WEB_PORT"
    case "$WEB_PORT:$WEB_REMOTE" in 80:true) printf 'WEB_FALLBACK=http://<bar-ip>:8080\n';; 8080:true) printf 'WEB_FALLBACK=http://<bar-ip>\n';; esac
    printf 'Remove USB, restart, and verify selected features and normal controls.\n'
    printf 'Startup receipt confirms service startup; wake behavior still needs checking.\n'
} > "$USB/RESULT.txt"
export_logs || finish INSTALLED_LOG_EXPORT_FAILED 1
finish INSTALLED_PENDING_REBOOT_TEST 0
