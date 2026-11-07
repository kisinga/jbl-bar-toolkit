#!/bin/sh
# Read-only standby watcher. Key input remains in the bounded, verified once.sh.
PATH=/bin:/sbin:/usr/bin:/usr/sbin:/system/bin:/system/xbin
export PATH
BB=/bin/busybox
# Source at the very start of an already-backgrounded helper; BB must be set.
# Release the command service's inherited sockets before any long-running work.
[ -x "$BB" ] || exit 1
exec </dev/null >/dev/null 2>&1
[ -d "/proc/$$/fd" ] || exit 1
for jbl_fd_path in /proc/$$/fd/*; do
    jbl_fd=${jbl_fd_path##*/}
    case "$jbl_fd" in ''|*[!0-9]*|0|1|2) continue;; esac
    # Glob enumeration can include its own directory FD, already closed here.
    # Closing earlier sockets also changes which FD command substitution uses.
    [ -L "$jbl_fd_path" ] || continue
    if ! jbl_target=$("$BB" readlink "$jbl_fd_path" 2>/dev/null); then
        [ ! -L "$jbl_fd_path" ] && continue
        exit 1
    fi
    case "$jbl_target" in
        socket:*) eval "exec ${jbl_fd}>&-" || exit 1;;
    esac
done
unset jbl_fd_path jbl_fd jbl_target

PROC=/proc
SOURCE=${0%/*}
STORE=/var/JBLStandard
RAM=/tmp/jbl-standard-watch-active
USB=/mnt/sda1/JBLInstall
EXPECTED_BDP=86ec83472a10dd4278285123f8ecbfb96bdd97444dd98c3a56d5d87441c90b63
[ -x "$BB" ] && [ -d "$STORE" ] || exit 1
[ ! -e "$STORE/DISABLED" ] && [ ! -e "$USB/ROLLBACK" ] || exit 0
"$BB" awk '$2=="/tmp" && $3=="tmpfs" {ok=1} END {exit !ok}' "$PROC/mounts" || exit 1
"$BB" mkdir "$RAM" 2>/dev/null || exit 0
child=
entries=0
last_event=
retain_lock=0
record() {
    [ "$1" != "$last_event" ] || return 0
    last_event=$1
    if [ "$entries" -ge 64 ]; then
        "$BB" mv "$RAM/watch.log" "$STORE/watch-previous.log"
        entries=0
    fi
    printf 'BOOT=%s UPTIME=%s EVENT=%s\n' "$boot" "$("$BB" cut -d ' ' -f 1 "$PROC/uptime")" "$1" >> "$RAM/watch.log"
    entries=$((entries + 1))
    "$BB" cp "$RAM/watch.log" "$STORE/watch.log"
    printf '%s\n' "$1" > "$STORE/WATCH_STATUS.txt"
}
finish() {
    reason=$1
    code=$2
    trap '' HUP INT TERM
    if [ -n "$child" ]; then
        kill -TERM "$child" 2>/dev/null
        wait "$child"
        child=
    fi
    if [ "$code" != 0 ]; then
        printf '%s\n' "$reason" > "$STORE/DISABLED" || retain_lock=1
    fi
    record "$reason"
    "$BB" sync
    [ "$retain_lock" = 1 ] || "$BB" rm -rf "$RAM"
    exit "$code"
}
trap 'finish INTERRUPTED 1' HUP INT TERM
boot=$("$BB" cat "$PROC/sys/kernel/random/boot_id")
[ ! -f "$STORE/watch.log" ] || "$BB" cp "$STORE/watch.log" "$STORE/watch-previous.log"
record START_REV5
(cd "$SOURCE" && "$BB" sha256sum -c checksums.txt) > "$RAM/integrity.log" 2>&1 || finish PAYLOAD_MISMATCH 1
PID=
START=
stable=0
handled=0
last_state=
read_value() {
    value=$("$BB" dd if="$PROC/$PID/mem" bs=1 skip="$1" count="$2" 2>/dev/null | "$BB" od -An -tu"$2" | "$BB" tr -d ' \n')
    case "$value" in ''|*[!0-9]*) return 1;; esac
    printf '%s\n' "$value"
}
while :; do
    [ ! -e "$STORE/DISABLED" ] && [ ! -e "$USB/ROLLBACK" ] || finish CANCELLED 0
    set -- $("$BB" pidof bdpprog)
    if [ "$#" -ne 1 ]; then
        stable=0; handled=0; PID=; START=
        record WAITING_FOR_PROCESS
        "$BB" sleep 5
        continue
    fi
    candidate=$1
    start=$("$BB" awk '{print $22}' "$PROC/$candidate/stat" 2>/dev/null)
    if [ -z "$start" ]; then stable=0; "$BB" sleep 5; continue; fi
    if [ "$candidate:$start" != "$PID:$START" ]; then
        hash=$("$BB" sha256sum "$PROC/$candidate/exe") || finish FIRMWARE_UNREADABLE 1
        set -- $hash
        [ "$1" = "$EXPECTED_BDP" ] || finish FIRMWARE_MISMATCH 1
        PID=$candidate; START=$start; stable=0; handled=0; last_state=
        record "PROCESS_${PID}_${START}"
    fi
    power=$(read_value 14191893 1) || finish POWER_UNREADABLE 1
    system=$(read_value 12962676 4) || finish SYSTEM_UNREADABLE 1
    mode=$(read_value 13378532 4) || finish MODE_UNREADABLE 1
    # Never combine samples from different process instances.
    now=$("$BB" awk '{print $22}' "$PROC/$PID/stat" 2>/dev/null)
    if [ "$now" != "$START" ]; then stable=0; "$BB" sleep 5; continue; fi
    state="$power:$system:$mode"
    [ "$state" = "$last_state" ] || stable=0
    last_state=$state
    case "$state" in
        1:0:0|1:0:3)
            if [ "$handled" = 0 ]; then
                stable=$((stable + 1))
                record "AWAKE_STABILIZING_$mode"
                if [ "$stable" -ge 3 ]; then
                    if [ "$mode" = 0 ]; then
                        handled=1
                        record ALREADY_STANDARD_THIS_WAKE
                    else
                        [ ! -e "$STORE/DISABLED" ] && [ ! -e "$USB/ROLLBACK" ] || finish CANCELLED 0
                        # Fresh status prevents a duplicate/skipped once call using old success.
                        "$BB" rm -f "$STORE/STATUS.txt"
                        record APPLYING_STANDARD
                        /bin/sh "$SOURCE/once.sh" "$STORE" &
                        child=$!
                        wait "$child"
                        code=$?
                        child=
                        result=$("$BB" cat "$STORE/STATUS.txt" 2>/dev/null)
                        [ "$code" = 0 ] || finish "ONCE_FAILED_$result" 1
                        case "$result" in
                            STANDARD_SELECTED|ALREADY_STANDARD_NO_CHANGE)
                                handled=1
                                record STANDARD_THIS_WAKE;;
                            NOT_READY_NO_CHANGE)
                                stable=0
                                record WAITING_FOR_POWER_ON;;
                            CANCELLED) finish CANCELLED 0;;
                            *) finish "UNEXPECTED_ONCE_RESULT_$result" 1;;
                        esac
                    fi
                fi
            fi;;
        *)
            stable=0; handled=0
            record "WAITING_POWER_${power}_SYSTEM_${system}_MODE_${mode}";;
    esac
    "$BB" sleep 5
done
