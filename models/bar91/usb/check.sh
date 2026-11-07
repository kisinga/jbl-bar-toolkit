#!/bin/sh
# Qualification preflight: temporary storage probes and read-only native checks.
USB=${JBL_TOOLKIT_USB:-/mnt/sda1/JBLToolkit}
. "$USB/common.sh" || exit 1
. "$USB/preflight.sh" || exit 1
. "$USB/legacy.sh" || exit 1
result=0
# Collect independent evidence even when the first installation guard fails.
# Never export credentials/configuration or execute an installed component.
{
    printf 'SCHEMA=1\nBUSYBOX_IDENTITY\n'
    "$BB" sha256sum "$BB"
    printf 'BUSYBOX_APPLETS\n'; "$BB" --list
    printf 'MV_HELP\n'; "$BB" mv --help 2>&1
    printf 'LN_HELP\n'; "$BB" ln --help 2>&1
    printf 'KERNEL_FILESYSTEMS\n'; "$BB" cat /proc/filesystems
    printf 'MOUNTS\n'; "$BB" cat /proc/mounts
    printf 'VAR_TARGET\n'; "$BB" readlink -f /var
    printf 'PERSISTENT_CAPACITY\n'; "$BB" df -Pk /var
    printf 'PAYLOAD_SIZE\n'; "$BB" du -sk "$USB/files"
    printf 'HOOK_IDENTITY\n'
    if regular "$HOOK"; then "$BB" sha256sum "$HOOK"; fi
    for path in "$STORE" "$SSH_STORE" "$REMOTE_STORE" /var/JBLStandard; do
        if directory "$path"; then
            printf 'STORE=%s\n' "$path"
            if regular "$path/OWNER.txt"; then "$BB" cat "$path/OWNER.txt"; fi
            # Hash selected scripts/snapshots for migration review. Do not read
            # passwords, private host keys, network settings or SSH config.
            for name in common.sh boot.sh start.sh install.sh uninstall.sh run.sh helper.sh controller.sh once.sh hook.expected hook.before hook.managed; do
                if regular "$path/$name"; then "$BB" sha256sum "$path/$name"; fi
            done
        fi
    done
} > "$USB/DIAGNOSTICS.txt" 2>&1
preflight_all || result=1
migration_eligible=false
if legacy_check; then
    migration_eligible=true
    printf 'MIGRATION_ELIGIBLE=true\nPROFILE=reviewed-%s-prototype-ssh-remote-chain\n' "$LEGACY_STANDARD_REVISION" > "$USB/MIGRATION_CHECK.txt"
else
    printf 'MIGRATION_ELIGIBLE=false\nREASON=%s\n' "$LEGACY_REASON" > "$USB/MIGRATION_CHECK.txt"
fi
native_result=${PREFLIGHT_NATIVE_RESULT:-skipped}
if [ "$native_result" = passed ] && [ "${PREFLIGHT_REASON-}" != PASSED ]; then
    native_result=passed_independent_of_failed_preflight
fi
{
    printf 'SCHEMA=1\nACTION=check\nBOOT_ID=%s\nPREFLIGHT=%s\n' "$(boot_id)" "${PREFLIGHT_REASON:-UNKNOWN}"
    printf 'NATIVE_CHECK=native-check.json\nNATIVE_RESULT=%s\nDIAGNOSTICS=DIAGNOSTICS.txt\nPREREQUISITES=PREFLIGHT.txt\n' "$native_result"
    "$BB" cat "$USB/PREFLIGHT.txt"
    printf 'MIGRATION_REVIEW=MIGRATION_CHECK.txt\n'
    if owned; then
        printf 'TOOLKIT=present\n'
        "$BB" cmp -s "$STORE/package.manifest" "$USB/SHA256SUMS" || printf 'UPDATE_AVAILABLE=true\n'
        runtime_integrity >/dev/null 2>&1 || { printf 'BLOCKER=INSTALLED_RUNTIME_INTEGRITY\n'; result=1; }
    else
        printf 'TOOLKIT=absent_or_unrecognized\n'
        for path in "$STORE" "$SSH_STORE" "$REMOTE_STORE" /var/JBLStandard; do
            if [ -e "$path" ] || [ -L "$path" ]; then
                if [ "$migration_eligible" = true ] && [ "$path" != "$STORE" ]; then
                    printf 'MIGRATION_SOURCE=%s\n' "$path"
                else
                    printf 'BLOCKER=EXISTING_STORE:%s\n' "$path"
                    result=1
                fi
            fi
        done
        if [ -e "$HOOK" ] || [ -L "$HOOK" ]; then
            if [ "$migration_eligible" != true ] && { ! regular "$HOOK" || "$BB" grep -Eq 'JBL(Standard|LocalSSH|Remote|Toolkit)' "$HOOK"; }; then
                printf 'BLOCKER=EXISTING_OR_UNSAFE_COMPONENT_HOOK\n'
                result=1
            fi
        fi
    fi
    printf 'DEVICE_SERVICES_CHANGED=false\nHARDWARE_QUALIFICATION=pending\n'
} > "$USB/CHECKS.txt"
"$BB" cat "$USB/CHECKS.txt"
if [ "$result" = 0 ]; then
    printf 'CHECK_PASSED_READY_FOR_INSTALL_OR_CONFIGURE\n' > "$USB/STATUS.txt"
else
    printf 'CHECK_BLOCKED_READ_CHECKS\n' > "$USB/STATUS.txt"
fi
"$BB" sync
exit "$result"
