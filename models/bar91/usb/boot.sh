#!/bin/sh
PATH=/bin:/sbin:/usr/bin:/usr/sbin
export PATH
unset LD_LIBRARY_PATH LD_PRELOAD
umask 077
ulimit -c 0
exec </dev/null >/dev/null 2>&1
STORE=${JBL_TOOLKIT_STORE:-/var/JBLToolkit}
. "$STORE/common.sh" || exit 1
release_sockets || exit 1
owned || exit 1
[ ! -e "$STORE/DISABLED" ] || exit 0
# A recovery drive already mounted during boot takes precedence over services.
if regular "$USB/RECOVER"; then
    marker "$STORE/DISABLED"
    exit 0
fi
"$BB" mkdir "$LOCK" 2>/dev/null || exit 0
trap '"$BB" rm -f "$LOCK/plan"; "$BB" rmdir "$LOCK"' EXIT
exec > "$STORE/boot.log" 2>&1
finish() { printf '%s\n' "$1" > "$STORE/BOOT_STATUS.txt"; "$BB" sync; exit "$2"; }
boot_id > "$STORE/boot.id" || finish BOOT_ID_FAILED 1
printf 'BOOT_STARTING\n' > "$STORE/BOOT_STATUS.txt"
runtime_integrity || finish BOOT_RUNTIME_INTEGRITY_FAILED 1
"$BB" awk -f "$STORE/parse-config.awk" "$STORE/config.txt" > "$LOCK/plan" || exit 1
. "$LOCK/plan" || exit 1
# A web remote that fails to start must not cost SSH, the network path for
# diagnosis and repair: record the failure, start SSH, then report it.
remote_failure=
if [ "$WEB_REMOTE" = true ] || [ "$AUTO_STANDARD" = true ]; then
    if /bin/sh "$REMOTE_STORE/start.sh"; then
        count=0
        while ! remote_ready; do
            [ "$count" -lt 15 ] || { remote_failure=BOOT_REMOTE_NOT_READY; break; }
            "$BB" sleep 1; count=$((count+1))
        done
    else
        remote_failure=BOOT_REMOTE_START_FAILED
    fi
    [ -z "$remote_failure" ] || printf '%s\n' "$remote_failure"
fi
if [ "$SSH" = true ]; then
    # Retain the shared lock through SSH's bounded network/startup wait.
    /bin/sh "$SSH_STORE/boot.sh" || finish BOOT_SSH_START_FAILED 1
    process_owned "$SSH_STORE/sshd.pid" "" ssh || finish BOOT_SSH_NOT_READY 1
fi
[ -z "$remote_failure" ] || finish "$remote_failure" 1
if [ "$WEB_REMOTE" = true ] || [ "$AUTO_STANDARD" = true ]; then
    remote_ready || finish BOOT_REMOTE_EXITED 1
fi
finish BOOT_SERVICES_READY 0
