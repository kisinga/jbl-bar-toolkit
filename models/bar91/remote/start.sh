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
# remote.pid survives reboots and power loss, so its number may now belong to
# any process, or the file may be empty. Only a live remote with this exact
# command line means it is already running; any other record is stale and is
# replaced. Nothing is ever signalled here.
if [ -f "$STORE/remote.pid" ]; then
    pid=$("$BB" cat "$STORE/remote.pid" 2>/dev/null)
    case "$pid" in
        ''|*[!0-9]*) ;;
        *) if kill -0 "$pid" 2>/dev/null; then
               cmd=$("$BB" tr '\000' ' ' < "/proc/$pid/cmdline" 2>/dev/null)
               case "$cmd" in "$STORE/remote -config $STORE/config.json "*) exit 0;; esac
           fi;;
    esac
fi
[ ! -f "$STORE/remote.log" ] || "$BB" mv "$STORE/remote.log" "$STORE/remote-previous.log"
# The remote moves a full remote.log to remote.log.1 (log_rotate.go): keep the
# previous run's next to remote-previous.log, never one from an older run.
"$BB" rm -f "$STORE/remote-previous.log.1" || exit 1
[ ! -f "$STORE/remote.log.1" ] || "$BB" mv "$STORE/remote.log.1" "$STORE/remote-previous.log.1"
"$BB" rm -f "$STORE/READY" || exit 1
"$STORE/remote" -config "$STORE/config.json" -ready-file "$STORE/READY" >"$STORE/remote.log" 2>&1 </dev/null &
printf '%s\n' "$!" > "$STORE/remote.pid"
