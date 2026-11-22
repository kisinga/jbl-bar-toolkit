#!/bin/sh
# Background startup entry: release vendor IPC sockets before becoming resident.
BB=/bin/busybox
unset LD_LIBRARY_PATH LD_PRELOAD
ulimit -c 0
exec </dev/null >/dev/null 2>&1
for p in /proc/$$/fd/*; do
    fd=${p##*/}
    case "$fd" in ''|*[!0-9]*|0|1|2) continue;; esac
    [ -L "$p" ] || continue
    target=$("$BB" readlink "$p") || { [ ! -L "$p" ] && continue; exit 1; }
    case "$target" in socket:*) eval "exec ${fd}>&-" || exit 1;; esac
done
STORE=/var/JBLRemote
[ "$("$BB" cat "$STORE/OWNER.txt")" = JBL_WEB_REMOTE_REV1 ] || exit 1
[ ! -e "$STORE/DISABLED" ] || exit 0
"$BB" mkdir /tmp/jbl-web-start-lock 2>/dev/null || exit 0
trap '"$BB" rmdir /tmp/jbl-web-start-lock' EXIT
if [ -f "$STORE/remote.pid" ]; then
    pid=$("$BB" cat "$STORE/remote.pid")
    case "$pid" in ''|*[!0-9]*) exit 1;; esac
    if kill -0 "$pid" 2>/dev/null; then
        cmd=$("$BB" tr '\000' ' ' < "/proc/$pid/cmdline")
        case "$cmd" in "$STORE/remote -config $STORE/config.json "*) exit 0;; *) exit 1;; esac
    fi
fi
[ ! -f "$STORE/remote.log" ] || "$BB" mv "$STORE/remote.log" "$STORE/remote-previous.log"
# The remote moves a full remote.log to remote.log.1 (log_rotate.go): keep the
# previous run's next to remote-previous.log, never one from an older run.
"$BB" rm -f "$STORE/remote-previous.log.1" || exit 1
[ ! -f "$STORE/remote.log.1" ] || "$BB" mv "$STORE/remote.log.1" "$STORE/remote-previous.log.1"
"$BB" rm -f "$STORE/READY" || exit 1
"$STORE/remote" -config "$STORE/config.json" -ready-file "$STORE/READY" >"$STORE/remote.log" 2>&1 </dev/null &
printf '%s\n' "$!" > "$STORE/remote.pid"
