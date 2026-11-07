#!/bin/sh
# One bounded invocation per firmware startup/wake callback. No resident poller.
PATH=/bin:/sbin:/usr/bin:/usr/sbin:/system/bin:/system/xbin
export PATH
BB=/bin/busybox
PROC=/proc
SOURCE=${0%/*}
STORE=${1:-/var/JBLStandard}
RAM=/tmp/jbl-standard-hook-active
USB=/mnt/sda1/JBLInstall
CLI=/bin/cli
EXPECTED_BDP=86ec83472a10dd4278285123f8ecbfb96bdd97444dd98c3a56d5d87441c90b63
EXPECTED_CLI=8910fb852d2f994a519615fede404fbb036e3a8f5264cc69cdff57038af3c78f
[ -x "$BB" ] && [ -d "$STORE" ] || exit 1
[ ! -e "$SOURCE/DISABLED" ] && [ ! -e "$USB/ROLLBACK" ] || exit 0
"$BB" awk '$2=="/tmp" && $3=="tmpfs" {ok=1} END {exit !ok}' "$PROC/mounts" || exit 1
"$BB" mkdir "$RAM" 2>/dev/null || exit 0
exec > "$RAM/run.log" 2>&1
child=
finish() {
    final=$1
    code=$2
    trap '' HUP INT TERM
    if [ -n "$child" ]; then
        kill -TERM "$child" 2>/dev/null
        wait "$child"
        child=
    fi
    printf 'RESULT=%s EXIT=%s\n' "$final" "$code"
    [ ! -f "$STORE/latest.log" ] || "$BB" cp "$STORE/latest.log" "$STORE/previous.log"
    "$BB" cp "$RAM/run.log" "$STORE/latest.log"
    printf '%s\n' "$final" > "$STORE/STATUS.txt"
    "$BB" rm -rf "$STORE/last-attempt"
    if [ -d "$RAM/attempt" ]; then
        "$BB" cp -R "$RAM/attempt" "$STORE/last-attempt"
    fi
    retain_lock=0
    if [ "$code" != 0 ] && [ "$STORE" = /var/JBLStandard ]; then
        printf '%s\n' "$final" > "$STORE/DISABLED" || retain_lock=1
    fi
    if [ -d "$USB" ]; then
        "$BB" mkdir -p "$USB/runtime"
        "$BB" cp "$STORE/latest.log" "$STORE/STATUS.txt" "$USB/runtime/"
        [ ! -f "$STORE/previous.log" ] || "$BB" cp "$STORE/previous.log" "$USB/runtime/"
        "$BB" rm -rf "$USB/runtime/last-attempt"
        if [ -d "$STORE/last-attempt" ]; then
            "$BB" cp -R "$STORE/last-attempt" "$USB/runtime/last-attempt"
        fi
    fi
    "$BB" sync
    [ "$retain_lock" = 1 ] || "$BB" rm -rf "$RAM"
    exit "$code"
}
trap 'finish INTERRUPTED 1' HUP INT TERM
printf 'JBL_STANDARD_ONCE_REV3 START\n'
printf 'BOOT_ID='; "$BB" cat "$PROC/sys/kernel/random/boot_id"
printf 'UPTIME='; "$BB" cat "$PROC/uptime"
(cd "$SOURCE" && "$BB" sha256sum -c checksums.txt) || finish PAYLOAD_MISMATCH 1
hash=$("$BB" sha256sum "$CLI") || finish CLI_UNREADABLE 1
set -- $hash
[ "$1" = "$EXPECTED_CLI" ] || finish CLI_MISMATCH 1
# Give early boot/audio initialization time to finish before sampling readiness.
"$BB" sleep 10
PID=
START=
stable=0
last=
i=0
read_value() {
    value=$("$BB" dd if="$PROC/$PID/mem" bs=1 skip="$1" count="$2" 2>/dev/null | "$BB" od -An -tu"$2" | "$BB" tr -d ' \n')
    case "$value" in ''|*[!0-9]*) return 1;; esac
    printf '%s\n' "$value"
}
while [ "$i" -lt 25 ]; do
    [ ! -e "$SOURCE/DISABLED" ] && [ ! -e "$USB/ROLLBACK" ] || finish CANCELLED 0
    i=$((i + 1))
    set -- $("$BB" pidof bdpprog)
    if [ "$#" -ne 1 ]; then stable=0; "$BB" sleep 2; continue; fi
    candidate=$1
    start=$("$BB" awk '{print $22}' "$PROC/$candidate/stat" 2>/dev/null)
    if [ -z "$start" ]; then stable=0; "$BB" sleep 2; continue; fi
    if [ "$candidate:$start" != "$PID:$START" ]; then
        hash=$("$BB" sha256sum "$PROC/$candidate/exe") || finish FIRMWARE_UNREADABLE 1
        set -- $hash
        [ "$1" = "$EXPECTED_BDP" ] || finish FIRMWARE_MISMATCH 1
        PID=$candidate; START=$start; stable=0
        printf 'PROCESS=%s START=%s\n' "$PID" "$START"
    fi
    power=$(read_value 14191893 1) || finish POWER_UNREADABLE 1
    system=$(read_value 12962676 4) || finish SYSTEM_UNREADABLE 1
    mode=$(read_value 13378532 4) || finish MODE_UNREADABLE 1
    state="$power:$system:$mode"
    if [ "$state" != "$last" ]; then
        printf 'POWER=%s SYSTEM=%s MODE=%s\n' "$power" "$system" "$mode"
        last=$state; stable=0
    fi
    case "$state" in 1:0:0|1:0:3) stable=$((stable + 1));; *) stable=0;; esac
    if [ "$stable" -ge 3 ]; then
        [ "$mode" != 0 ] || finish ALREADY_STANDARD_NO_CHANGE 0
        "$BB" mkdir -p "$RAM/attempt/JBLTest" || finish RAM_SETUP_FAILED 1
        "$BB" cp "$SOURCE/controller.sh" "$RAM/controller.sh" || finish RAM_SETUP_FAILED 1
        "$BB" cp "$SOURCE/helper.sh" "$RAM/attempt/JBLTest/run.sh" || finish RAM_SETUP_FAILED 1
        /bin/sh "$RAM/controller.sh" "$RAM/attempt" &
        child=$!
        wait "$child"
        code=$?
        child=
        result=$("$BB" cat "$RAM/attempt/STATUS.txt" 2>/dev/null)
        printf 'CONTROLLER=%s EXIT=%s\n' "$result" "$code"
        case "$result:$code" in
            STANDARD_SELECTED:0|ALREADY_STANDARD_NO_CHANGE:0) finish "$result" 0;;
            *) finish "STOPPED_$result" 1;;
        esac
    fi
    "$BB" sleep 2
done
finish NOT_READY_NO_CHANGE 0
