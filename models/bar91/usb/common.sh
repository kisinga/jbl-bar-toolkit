#!/bin/sh
BB=${JBL_BUSYBOX:-/bin/busybox}
USB=${JBL_TOOLKIT_USB:-/mnt/sda1/JBLToolkit}
STORE=${JBL_TOOLKIT_STORE:-/var/JBLToolkit}
SSH_STORE=${JBL_SSH_STORE:-/var/JBLLocalSSH}
REMOTE_STORE=${JBL_REMOTE_STORE:-/var/JBLRemote}
STANDARD_STORE=${JBL_STANDARD_STORE:-/var/JBLStandard}
LEGACY_STORE=${JBL_LEGACY_STORE:-/var/JBLToolkitLegacy}
HOOK=${JBL_TOOLKIT_HOOK:-/var/AutoScript.TSS}
LOCK=${JBL_TOOLKIT_LOCK:-/tmp/jbl-toolkit-operation}
OWNER=JBL_TOOLKIT_REV1
regular() { [ -f "$1" ] && [ ! -L "$1" ]; }
directory() { [ -d "$1" ] && [ ! -L "$1" ]; }
owned() { directory "$STORE" && regular "$STORE/OWNER.txt" && [ "$("$BB" cat "$STORE/OWNER.txt")" = "$OWNER" ]; }
component_owned() {
    directory "$1" && regular "$1/OWNER.txt" && [ "$("$BB" cat "$1/OWNER.txt")" = "$2" ]
}
legacy_archive_owned() { component_owned "$LEGACY_STORE" JBL_TOOLKIT_LEGACY_REV1; }
boot_id() { "$BB" cat /proc/sys/kernel/random/boot_id; }
remote_ready() {
    process_owned "$REMOTE_STORE/remote.pid" "$REMOTE_STORE/remote -config $REMOTE_STORE/config.json " &&
    regular "$REMOTE_STORE/READY" && [ "$("$BB" cat "$REMOTE_STORE/READY")" = "$process_pid" ]
}
probe_native() {
    # FAT32 can be mounted noexec. Execute the pinned probe from our private
    # operation directory, never change a device store merely to test readiness.
    regular "$USB/files/remote" || return 1
    [ ! -e "$LOCK/inspect-remote" ] && [ ! -L "$LOCK/inspect-remote" ] || return 1
    "$BB" cp "$USB/files/remote" "$LOCK/inspect-remote" && "$BB" chmod 700 "$LOCK/inspect-remote" || return 1
    if [ "${1-}" = awake ]; then
        # Some bars run AutoScript only at boot, before the native state is On.
        awake_wait=0
        while :; do
            "$BB" timeout 20 "$LOCK/inspect-remote" -inspect-device -require-awake > "$USB/native-check.json"
            probe_result=$?
            [ "$probe_result" -ne 0 ] && [ "$awake_wait" -lt 36 ] || break
            ! "$BB" grep -q '"power":"On"' "$USB/native-check.json" || break
            awake_wait=$((awake_wait+1))
            "$BB" sleep 5
        done
    else
        "$BB" timeout 20 "$LOCK/inspect-remote" -inspect-device > "$USB/native-check.json"
        probe_result=$?
    fi
    "$BB" rm -f "$LOCK/inspect-remote"
    return "$probe_result"
}
runtime_integrity() {
    regular "$STORE/runtime.manifest" && ("$BB" sha256sum -c "$STORE/runtime.manifest")
}
publish_directory() {
    regular "$USB/files/remote" || return 1
    [ ! -e "$LOCK/publish-remote" ] && [ ! -L "$LOCK/publish-remote" ] || return 1
    "$BB" cp "$USB/files/remote" "$LOCK/publish-remote" && "$BB" chmod 700 "$LOCK/publish-remote" || return 1
    "$BB" timeout 20 "$LOCK/publish-remote" -publish-directory "$1" "$2"
    publish_result=$?
    "$BB" rm -f "$LOCK/publish-remote"
    return "$publish_result"
}
release_sockets() {
    for path in /proc/$$/fd/*; do
        fd=${path##*/}
        case "$fd" in ''|*[!0-9]*|0|1|2) continue;; esac
        [ -L "$path" ] || continue
        target=$("$BB" readlink "$path") || { [ ! -L "$path" ] && continue; return 1; }
        case "$target" in socket:*) eval "exec ${fd}>&-" || return 1;; esac
    done
}
# No overwriting symlinks, directories, or leftovers from an interrupted copy.
atomic_copy() {
    regular "$1" || return 1
    [ ! -e "$2" ] && [ ! -L "$2" ] || regular "$2" || return 1
    [ ! -e "$2.new" ] && [ ! -L "$2.new" ] || return 1
    # Same-package configuration need not allocate a second multi-megabyte
    # binary. Keep private permissions even when an identical copy is reused.
    if regular "$2" && "$BB" cmp -s "$1" "$2"; then
        "$BB" chmod 600 "$2"
        return "$?"
    fi
    if "$BB" cp "$1" "$2.new" && "$BB" chmod 600 "$2.new" && "$BB" mv "$2.new" "$2"; then
        return 0
    fi
    [ ! -f "$2.new" ] || "$BB" rm -f "$2.new"
    return 1
}
marker() {
    [ ! -e "$1" ] && [ ! -L "$1" ] || regular "$1" || return 1
    printf '%s\n' "$OWNER" > "$1"
}
process_alive() {
    kill -0 "$1" 2>/dev/null || return 1
    # A daemon adopted by init can remain a zombie briefly after clean shutdown.
    "$BB" awk '{sub(/^.*\) /, ""); exit ($1=="Z")}' "/proc/$1/stat" 2>/dev/null
}
process_owned() {
    regular "$1" || return 1
    process_pid=$("$BB" cat "$1")
    case "$process_pid" in ''|*[!0-9]*|0|1) return 1;; esac
    [ -r "/proc/$process_pid/cmdline" ] || return 1
    process_cmd=$("$BB" tr '\000' ' ' < "/proc/$process_pid/cmdline") || return 1
    if [ "${3-}" = ssh ]; then
        [ "$("$BB" readlink "/proc/$process_pid/exe")" = "$("$BB" readlink -f /usr/local/sbin/sshd)" ] || return 1
        case "$process_cmd" in *"sshd -D -E $SSH_STORE/sshd.log -f $SSH_STORE/sshd_config"*) return 0;; esac
    else
        case "$process_cmd" in "$2"*) return 0;; esac
    fi
    return 1
}
stop_remote() {
    [ ! -e "$REMOTE_STORE" ] && [ ! -L "$REMOTE_STORE" ] && return 0
    component_owned "$REMOTE_STORE" JBL_WEB_REMOTE_REV1 || return 1
    regular "$REMOTE_STORE/TOOLKIT" && [ "$("$BB" cat "$REMOTE_STORE/TOOLKIT")" = "$OWNER" ] || return 1
    marker "$REMOTE_STORE/DISABLED" || return 1
    if process_owned "$REMOTE_STORE/remote.pid" "$REMOTE_STORE/remote -config $REMOTE_STORE/config.json "; then
        kill -TERM "$process_pid" || return 1
        stop_count=0
        while process_alive "$process_pid"; do
            [ "$stop_count" -lt 40 ] || return 1
            "$BB" sleep 1; stop_count=$((stop_count+1))
        done
    elif regular "$REMOTE_STORE/remote.pid"; then
        process_pid=$("$BB" cat "$REMOTE_STORE/remote.pid")
        case "$process_pid" in ''|*[!0-9]*|0|1) return 1;; esac
        # A live unrelated PID is preserved and reported, never signalled.
        ! process_alive "$process_pid" || return 1
    fi
    [ ! -e "$REMOTE_STORE/remote.pid" ] || "$BB" rm "$REMOTE_STORE/remote.pid" || return 1
    return 0
}
stop_ssh() {
    [ ! -e "$SSH_STORE" ] && [ ! -L "$SSH_STORE" ] && return 0
    component_owned "$SSH_STORE" JBL_LOCAL_SSH_REV1 || return 1
    regular "$SSH_STORE/TOOLKIT" && [ "$("$BB" cat "$SSH_STORE/TOOLKIT")" = "$OWNER" ] || return 1
    marker "$SSH_STORE/BOOT_DISABLED" || return 1
    if process_owned "$SSH_STORE/sshd.pid" "" ssh; then
        kill -TERM "$process_pid" || return 1
        stop_count=0
        while process_alive "$process_pid"; do
            [ "$stop_count" -lt 15 ] || return 1
            "$BB" sleep 1; stop_count=$((stop_count+1))
        done
    elif regular "$SSH_STORE/sshd.pid"; then
        process_pid=$("$BB" cat "$SSH_STORE/sshd.pid")
        case "$process_pid" in ''|*[!0-9]*|0|1) return 1;; esac
        ! process_alive "$process_pid" || return 1
    fi
    [ ! -e "$SSH_STORE/sshd.pid" ] || "$BB" rm "$SSH_STORE/sshd.pid" || return 1
    return 0
}
