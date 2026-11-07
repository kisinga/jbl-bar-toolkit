#!/bin/sh
# Run before creating stores, changing networking, stopping services or gestures.
preflight_root() {
    PREFLIGHT_REASON=ROOT_REQUIRED
    [ "$("$BB" id -u)" = 0 ] || return 1
}

preflight_busybox() {
    PREFLIGHT_REASON=UNSUPPORTED_BUSYBOX_APPLETS_OR_ATOMIC_FILE_OPERATIONS
    applets=$("$BB" --list) || return 1
    for app in awk sha256sum cp chmod mkdir rm rmdir readlink cmp sync tr pidof grep timeout sleep cat stat mv ln mktemp du df wc find; do
        PREFLIGHT_REASON=MISSING_BUSYBOX_APPLET_$app
        printf '%s\n' "$applets" | "$BB" grep -qx "$app" || return 1
    done
    PREFLIGHT_REASON=BUSYBOX_LN_NO_TARGET_DIRECTORY_UNAVAILABLE
    "$BB" ln --help 2>&1 | "$BB" grep -q -- '-T' || return 1
}

preflight_usb() {
    PREFLIGHT_REASON=USB_NOT_FIRST_PARTITION_WRITABLE_FAT32
    "$BB" awk '
      $2=="/mnt/sda1" && ($1=="/dev/sda1" || $1=="/dev/block/sda1") && $3=="vfat" && index("," $4 ",",",rw,") {ok=1}
      END {exit !ok}
    ' /proc/mounts || return 1
}

preflight_network_stage() {
    PREFLIGHT_REASON=SSH_STAGING_DIRECTORY_UNSAFE
    # The manager owns $stage for transaction cleanup. Keep probe variables
    # isolated so its finish handler never removes the network report directory.
    (
        stage=${USB%/JBLToolkit}
        suffix=${stage#/tmp/jbl-toolkit-ssh.}
        [ "$stage" != "$suffix" ] && [ "${#suffix}" = 32 ] || return 1
        case "$suffix" in *[!0-9a-f]*) return 1;; esac
        [ "$USB" = "$stage/JBLToolkit" ] || return 1
        for dir in "$stage" "$USB"; do
            directory "$dir" && [ "$("$BB" stat -c %u "$dir")" = 0 ] &&
                [ "$("$BB" stat -c %a "$dir")" = 700 ] || return 1
        done
        resolved=$("$BB" readlink -f /tmp) || return 1
        [ "$("$BB" readlink -f "$stage")" = "$resolved/jbl-toolkit-ssh.$suffix" ] || return 1
        owned || return 1
    )
}

preflight_persistent() {
    PREFLIGHT_REASON=VAR_NOT_EXPECTED_PERSISTENT_PATH
    resolved=$("$BB" readlink -f /var) || return 1
    [ "$resolved" = /mnt/ubi_boot/var ] || return 1
    PREFLIGHT_REASON=VAR_NOT_WRITABLE_UBIFS
    "$BB" awk -v target="$resolved" '
      ($2=="/" || target==$2 || index(target,$2 "/")==1) && length($2)>best {
        best=length($2); point=$2; type=$3; options="," $4 ","
      }
      END {exit !(point=="/mnt/ubi_boot" && type=="ubifs" && index(options,",rw,"))}
    ' /proc/mounts || return 1
}

preflight_firmware() {
    PREFLIGHT_REASON=NO_SINGLE_RUNNING_BDPPROG
    pids=$("$BB" pidof bdpprog) || return 1
    case "$pids" in ''|*[!0-9]*) return 1;; esac
    PREFLIGHT_REASON=BDPPROG_IDENTITY_MISMATCH_OR_UNREADABLE
    hash=$("$BB" sha256sum "/proc/$pids/exe") || return 1
    [ "${hash%% *}" = 86ec83472a10dd4278285123f8ecbfb96bdd97444dd98c3a56d5d87441c90b63 ] || return 1
    PREFLIGHT_REASON=CLI_IDENTITY_MISMATCH_OR_UNREADABLE
    hash=$("$BB" sha256sum /bin/cli) || return 1
    [ "${hash%% *}" = 8910fb852d2f994a519615fede404fbb036e3a8f5264cc69cdff57038af3c78f ] || return 1
}

preflight_ssh() {
    if [ "$SSH" = true ]; then
        PREFLIGHT_REASON=UNSUPPORTED_NATIVE_SSH_TOOLS_OR_RUNTIME
        # Isolate SSH helper names/STORE from the manager's own namespace.
        (JBL_SSH_STORE="$SSH_STORE"; export JBL_SSH_STORE; . "$USB/files/ssh/common.sh" && verify_tools) || return 1
        PREFLIGHT_REASON=SSH_CREDENTIALS_REQUIRED
        [ -n "${SSH_PASSWORD_HASH-}" ] || regular "$USB/files/ssh/password.hash" || return 1
        preflight_ssh_port || return 1
    fi
}

# A standalone sshd from a removed or moved-aside installation keeps listening
# on 2222 until the bar restarts, and the new listener then fails to bind.
# Accept a listener only when it is the sshd recorded by the SSH store: the
# reviewed migration source that migrate.sh stops, or the toolkit's own daemon.
preflight_ssh_port() {
    PREFLIGHT_REASON=SSH_PORT_STATE_UNREADABLE
    [ -r /proc/net/tcp ] || return 1
    # Local port 2222 is 08AE; state 0A is LISTEN. tcp6 is absent without IPv6.
    if "$BB" cat /proc/net/tcp /proc/net/tcp6 2>/dev/null |
       "$BB" awk '$4=="0A" && $2 ~ /:08AE$/ {found=1} END {exit !found}'; then
        PREFLIGHT_REASON=SSH_PORT_2222_IN_USE_RESTART_BAR_FIRST
        # Subshell: process_owned sets process_pid, which the manager reuses.
        (process_owned "$SSH_STORE/sshd.pid" "" ssh) || return 1
    fi
    return 0
}

# Source FAT allocation units can be 64 KiB; they do not describe UBIFS cost.
# Budget file bytes rounded up to 4 KiB, plus the existing staging/state reserve.
payload_size_kib() (
    trap '"$BB" rm -f "$LOCK/space-files" "$LOCK/space-sizes"' EXIT
    "$BB" find "$1" -type f > "$LOCK/space-files" || return 1
    while IFS= read -r file; do
        "$BB" stat -c %s "$file" || return 1
    done < "$LOCK/space-files" > "$LOCK/space-sizes" || return 1
    "$BB" awk '
      /^[0-9]+$/ {total += int(($0+4095)/4096)*4; next}
      {bad=1}
      END {if (bad || NR==0) exit 1; print total}
    ' "$LOCK/space-sizes"
)

preflight_space() {
    # One new persistent payload copy plus 2 MiB for manager/SSH staging, keys
    # and runtime state. Existing files are already counted as used by df;
    # publication/inspection executables run from the private /tmp lock in RAM.
    PREFLIGHT_REASON=FREE_SPACE_UNREADABLE_OR_INSUFFICIENT
    payload_kib=$(payload_size_kib "$USB/files") || return 1
    required_kib=$((payload_kib+2048))
    reuse_remote=false
    reused_kib=0
    if { [ "${WEB_REMOTE:-true}" != true ] && [ "${AUTO_STANDARD:-true}" != true ]; } ||
       { directory "$REMOTE_STORE" && regular "$REMOTE_STORE/TOOLKIT" && [ "$("$BB" cat "$REMOTE_STORE/TOOLKIT")" = JBL_TOOLKIT_REV1 ] &&
         regular "$REMOTE_STORE/remote" && regular "$USB/files/remote" && "$BB" cmp -s "$REMOTE_STORE/remote" "$USB/files/remote"; }; then
        remote_kib=$(payload_size_kib "$USB/files/remote") || return 1
        [ "$remote_kib" -le "$payload_kib" ] || return 1
        reused_kib=$remote_kib
        reuse_remote=true
    fi
    # atomic_copy reuses identical staging/support files too. Credit only
    # verified regular files in our managed stores; retain the full reserve
    # for SSH's second copy, account files, scripts and filesystem overhead.
    if owned && directory "$STORE/ssh-input"; then
        for source in "$USB"/files/ssh/*.sh; do
            target=$STORE/ssh-input/${source##*/}
            if regular "$source" && regular "$target" && "$BB" cmp -s "$source" "$target"; then
                file_kib=$(payload_size_kib "$source") || return 1
                reused_kib=$((reused_kib+file_kib))
            fi
        done
    fi
    if directory "$REMOTE_STORE" && regular "$REMOTE_STORE/TOOLKIT" && [ "$("$BB" cat "$REMOTE_STORE/TOOLKIT")" = JBL_TOOLKIT_REV1 ] &&
       regular "$USB/files/remote-start.sh" && regular "$REMOTE_STORE/start.sh" &&
       "$BB" cmp -s "$USB/files/remote-start.sh" "$REMOTE_STORE/start.sh"; then
        file_kib=$(payload_size_kib "$USB/files/remote-start.sh") || return 1
        reused_kib=$((reused_kib+file_kib))
    fi
    [ "$reused_kib" -le "$payload_kib" ] || return 1
    required_kib=$((payload_kib-reused_kib+2048))
    available=$("$BB" df -Pk /var | "$BB" awk 'NR==2 {print $4}') || return 1
    case "$available" in ''|*[!0-9]*) return 1;; esac
    printf 'SPACE_PAYLOAD_KIB=%s SPACE_REQUIRED_KIB=%s SPACE_AVAILABLE_KIB=%s REUSE_REMOTE=%s SPACE_REUSED_KIB=%s\n' "$payload_kib" "$required_kib" "$available" "$reuse_remote" "$reused_kib"
    [ "$available" -ge "$required_kib" ] || return 1
}

preflight_boot_id() {
    PREFLIGHT_REASON=BOOT_ID_UNREADABLE
    [ -n "$(boot_id)" ] || return 1
}

preflight_atomic_io() {
    PREFLIGHT_REASON=USB_OR_PERSISTENT_STORAGE_ATOMIC_IO_FAILED
    for target in "$USB" /var; do
        probe=$("$BB" mktemp -d "$target/.jbl-toolkit-check.XXXXXX") || return 1
        if ! printf 'JBL_TOOLKIT_STORAGE_CHECK\n' > "$probe/first" ||
           ! "$BB" cp "$probe/first" "$probe/second" ||
           ! "$BB" cmp -s "$probe/first" "$probe/second" ||
           ! "$BB" mv "$probe/second" "$probe/renamed" ||
           ! "$BB" cmp -s "$probe/first" "$probe/renamed"; then
            "$BB" rm -rf "$probe"
            return 1
        fi
        if [ "$target" = /var ]; then
            PREFLIGHT_REASON=PERSISTENT_STORAGE_ATOMIC_NO_REPLACE_UNAVAILABLE
            "$BB" mkdir "$probe/published.stage.probe" || return 1
            if ! publish_directory "$probe/published.stage.probe" "$probe/published"; then
                "$BB" rm -rf "$probe"
                return 1
            fi
            # A second publication must fail and preserve both directories.
            "$BB" mkdir "$probe/published.stage.conflict" || return 1
            if publish_directory "$probe/published.stage.conflict" "$probe/published" >/dev/null 2>&1 ||
               [ ! -d "$probe/published.stage.conflict" ] || [ ! -d "$probe/published" ]; then
                "$BB" rm -rf "$probe"
                return 1
            fi
        fi
        "$BB" rm -rf "$probe" || return 1
    done
}

preflight_native() {
    PREFLIGHT_REASON=NATIVE_EXECUTION_STATE_OR_HELPER_LIBRARIES_NOT_QUALIFIED
    probe_native awake || return 1
}

# Installation stops at the first failed prerequisite. Check-only mode uses the
# same guards but reports every independent result without changing services.
preflight_steps() {
    case "${JBL_TOOLKIT_TRANSPORT:-usb}" in
        usb) source_step=usb;;
        ssh) source_step=network_stage;;
        *) return 1;;
    esac
    printf 'root busybox %s persistent firmware ssh space boot_id atomic_io native\n' "$source_step"
}
preflight() {
    steps=$(preflight_steps) || return 1
    for step in $steps; do
        "preflight_$step" || return 1
    done
    PREFLIGHT_REASON=PASSED
}
preflight_all() {
    all_result=0
    first_failure=
    root_ok=0; busybox_ok=0; usb_ok=0; persistent_ok=0
    PREFLIGHT_NATIVE_RESULT=skipped
    : > "$USB/PREFLIGHT.txt" || return 1
    steps=$(preflight_steps) || return 1
    for step in $steps; do
        if { [ "$step" = atomic_io ] && [ "$root_ok:$busybox_ok:$usb_ok:$persistent_ok" != 1:1:1:1 ]; } ||
           { [ "$step" = native ] && [ "$root_ok:$busybox_ok" != 1:1 ]; }; then
            printf 'SKIP=%s:PREREQUISITES_FAILED\n' "$step" >> "$USB/PREFLIGHT.txt"
            all_result=1
            continue
        fi
        PREFLIGHT_REASON=UNKNOWN
        if "preflight_$step"; then
            printf 'PASS=%s\n' "$step" >> "$USB/PREFLIGHT.txt"
            case "$step" in
                root) root_ok=1;; busybox) busybox_ok=1;; usb|network_stage) usb_ok=1;;
                persistent) persistent_ok=1;; native) PREFLIGHT_NATIVE_RESULT=passed;;
            esac
        else
            printf 'FAIL=%s:%s\n' "$step" "$PREFLIGHT_REASON" >> "$USB/PREFLIGHT.txt"
            [ -n "$first_failure" ] || first_failure=$PREFLIGHT_REASON
            [ "$step" != native ] || PREFLIGHT_NATIVE_RESULT=failed
            all_result=1
        fi
    done
    PREFLIGHT_REASON=${first_failure:-PASSED}
    return "$all_result"
}
