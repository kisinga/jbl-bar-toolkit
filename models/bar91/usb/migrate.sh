#!/bin/sh
# Explicit retirement of one exact reviewed standalone chain, followed by the
# normal installer. Keep old files inactive on the same persistent filesystem.
USB=${JBL_TOOLKIT_USB:-/mnt/sda1/JBLToolkit}
. "$USB/common.sh" || exit 1
. "$USB/preflight.sh" || exit 1
. "$USB/legacy.sh" || exit 1
umask 077
legacy_stage=
mutation_lock=/tmp/jbl-standard-mutation-active
locked=0
finish() {
    trap '' HUP INT TERM
    [ -z "$legacy_stage" ] || [ ! -d "$legacy_stage" ] || "$BB" rm -rf "$legacy_stage"
    [ "$locked" != 1 ] || "$BB" rmdir "$mutation_lock"
    printf '%s\n' "$1" > "$USB/STATUS.txt"
    printf 'RESULT=%s EXIT=%s\n' "$1" "$2"
    printf 'RESULT=%s\nARCHIVE=%s\nREBOOT_REQUIRED_ON_FAILURE=%s\n' "$1" "$LEGACY_STORE" "$2" > "$USB/MIGRATION.txt"
    "$BB" sync
    exit "$2"
}
trap 'finish MIGRATION_INTERRUPTED_RETAIN_ARCHIVE_AND_REBOOT 1' HUP INT TERM
"$BB" awk -f "$USB/parse-config.awk" "$USB/config.txt" > "$LOCK/plan" || finish CONFIG_INVALID 1
. "$LOCK/plan" || finish CONFIG_INVALID 1
"$BB" rm "$LOCK/plan"
case "$ACTION" in migrate|update) ;; *) finish INVALID_MIGRATION_ACTION 1;; esac
[ "$SSH" != true ] || [ -n "${SSH_PASSWORD_HASH-}" ] || regular "$USB/files/ssh/password.hash" || finish MIGRATION_SSH_CREDENTIALS_REQUIRED_NO_CHANGES 1
if owned; then
    legacy_archive_owned && regular "$LEGACY_STORE/RETIRED" &&
    "$BB" cmp -s "$STORE/package.manifest" "$USB/SHA256SUMS" || finish MIGRATION_TOOLKIT_CONFLICT 1
    finish ALREADY_MIGRATED 0
fi
[ ! -e "$STORE" ] && [ ! -L "$STORE" ] || finish MIGRATION_TOOLKIT_STORE_CONFLICT 1
legacy_check || { printf 'LEGACY_REASON=%s\n' "$LEGACY_REASON"; finish MIGRATION_IDENTITY_FAILED_NO_CHANGES 1; }
preflight || { printf 'PREFLIGHT_REASON=%s\n' "$PREFLIGHT_REASON"; finish MIGRATION_PREFLIGHT_FAILED_NO_CHANGES 1; }
"$BB" mv --help 2>&1 | "$BB" grep -q -- '-n' || finish MIGRATION_NO_REPLACE_MOVE_UNAVAILABLE_NO_CHANGES 1
"$BB" mkdir "$mutation_lock" 2>/dev/null || finish MIGRATION_LEGACY_OPERATION_BUSY 1
locked=1
if ! legacy_archive_owned; then
    legacy_stage=$("$BB" mktemp -d "$LEGACY_STORE.stage.XXXXXX") || finish MIGRATION_ARCHIVE_CREATE_FAILED 1
    printf 'JBL_TOOLKIT_LEGACY_REV1\n' > "$legacy_stage/OWNER.txt" || finish MIGRATION_ARCHIVE_CREATE_FAILED 1
    atomic_copy "$HOOK" "$legacy_stage/hook.legacy" || finish MIGRATION_HOOK_SNAPSHOT_FAILED 1
    "$BB" stat -c %a "$HOOK" > "$legacy_stage/hook.mode" || finish MIGRATION_HOOK_SNAPSHOT_FAILED 1
    "$BB" sync
    publish_directory "$legacy_stage" "$LEGACY_STORE" || finish MIGRATION_ARCHIVE_PUBLISH_FAILED 1
    legacy_stage=
fi
# Reject races before the first disable marker; never execute legacy code.
legacy_check || finish MIGRATION_IDENTITIES_CHANGED_PRESERVED 1
marker "$LEGACY_REMOTE/DISABLED" && marker "$LEGACY_SSH/BOOT_DISABLED" && marker "$LEGACY_STANDARD/DISABLED" || finish MIGRATION_DISABLE_FAILED 1
# Stop only the owned listener/remote. Unrelated live PIDs are preserved.
legacy_stop() {
    legacy_pidfile=$1; legacy_kind=$2
    [ -e "$legacy_pidfile" ] || return 0
    if [ "$legacy_kind" = ssh ]; then
        process_owned "$legacy_pidfile" "" ssh
    else
        process_owned "$legacy_pidfile" "$REMOTE_STORE/remote -config $REMOTE_STORE/config.json "
    fi
    if [ "$?" = 0 ]; then
        kill -TERM "$process_pid" || return 1
        legacy_wait=0
        while process_alive "$process_pid"; do
            [ "$legacy_wait" -lt 40 ] || return 1
            "$BB" sleep 1; legacy_wait=$((legacy_wait+1))
        done
    else
        legacy_pid=$("$BB" cat "$legacy_pidfile")
        case "$legacy_pid" in ''|*[!0-9]*|0|1) return 1;; esac
        ! process_alive "$legacy_pid" || return 1
    fi
}
if [ "$LEGACY_REMOTE" = "$REMOTE_STORE" ]; then
    legacy_stop "$REMOTE_STORE/remote.pid" remote || finish MIGRATION_REMOTE_STOP_FAILED_REBOOT 1
fi
# Graceful Go shutdown can release its Smart Mode disable marker. Reassert it.
marker "$LEGACY_STANDARD/DISABLED" || finish MIGRATION_DISABLE_FAILED 1
if [ "$LEGACY_SSH" = "$SSH_STORE" ]; then
    legacy_stop "$SSH_STORE/sshd.pid" ssh || finish MIGRATION_SSH_STOP_FAILED_REBOOT 1
fi
# Existing workers must finish naturally, including console/key restoration.
legacy_wait=0
while [ -d /tmp/jbl-standard-hook-active ] || [ -d /tmp/jbl-standard-watch-active ] ||
      [ -d /tmp/jbl-local-ssh-boot-lock ] || [ -d /tmp/jbl-local-ssh-setup-lock ] || [ -d /tmp/jbl-web-start-lock ]; do
    [ "$legacy_wait" -lt 190 ] || finish MIGRATION_WORKERS_BUSY_REBOOT 1
    "$BB" sleep 1; legacy_wait=$((legacy_wait+1))
done
legacy_check || finish MIGRATION_HOOK_OR_FILES_CHANGED_PRESERVED 1
# The reviewed rev5/rev6 base hook has no earlier vendor/custom hook to restore.
if regular "$HOOK"; then "$BB" rm "$HOOK" || finish MIGRATION_HOOK_REMOVE_FAILED 1; fi
"$BB" sync
for legacy_spec in "$REMOTE_STORE:JBLRemote" "$SSH_STORE:JBLLocalSSH" "$STANDARD_STORE:JBLStandard"; do
    legacy_live=${legacy_spec%:*}; legacy_name=${legacy_spec##*:}
    if [ -e "$legacy_live" ]; then
        [ ! -e "$LEGACY_STORE/$legacy_name" ] && [ ! -L "$LEGACY_STORE/$legacy_name" ] || finish MIGRATION_ARCHIVE_ENTRY_CONFLICT 1
        # The destination is an owned, private existing directory. -n prevents
        # replacement of a child, and the live-path check detects a refused move.
        "$BB" mv -n "$legacy_live" "$LEGACY_STORE/" || finish MIGRATION_ARCHIVE_MOVE_FAILED 1
        [ ! -e "$legacy_live" ] && [ ! -L "$legacy_live" ] || finish MIGRATION_ARCHIVE_MOVE_REFUSED 1
    fi
done
legacy_check || finish MIGRATION_ARCHIVE_VERIFICATION_FAILED 1
marker "$LEGACY_STORE/RETIRED" || finish MIGRATION_RETIRE_RECEIPT_FAILED 1
finish LEGACY_RETIRED_CONTINUING_WITH_INSTALL 0
