#!/bin/sh
# Narrow, reviewed migration profile. This does not infer ownership from names
# or execute scripts/binaries found in an old installation.
legacy_hash() {
    regular "$1" || { printf 'LEGACY_FILE_MISSING_OR_UNSAFE=%s\n' "$1"; return 1; }
    legacy_digest=$("$BB" sha256sum "$1") || return 1
    [ "${legacy_digest%% *}" = "$2" ] || { printf 'LEGACY_FILE_HASH_MISMATCH=%s\n' "$1"; return 1; }
}
legacy_tree() {
    legacy_special=$("$BB" find "$1" ! -type d ! -type f) || return 1
    [ -z "$legacy_special" ]
}
legacy_locate() {
    legacy_path=$1
    if [ -e "$LEGACY_STORE/$2" ] || [ -L "$LEGACY_STORE/$2" ]; then
        [ ! -e "$1" ] && [ ! -L "$1" ] || return 1
        legacy_path=$LEGACY_STORE/$2
    fi
    directory "$legacy_path"
}
legacy_check() {
    LEGACY_REASON=LEGACY_ARCHIVE_CONFLICT
    if [ -e "$LEGACY_STORE" ] || [ -L "$LEGACY_STORE" ]; then
        legacy_archive_owned || return 1
        legacy_tree "$LEGACY_STORE" || return 1
        legacy_hash "$LEGACY_STORE/hook.legacy" 5d2f06b6b73ef1ef4ae8a2363801190e768c3c4486ed81cbe88e4c267b34c15d || return 1
    fi
    LEGACY_REASON=LEGACY_STORES_MISSING_OR_CONFLICTING
    legacy_locate "$REMOTE_STORE" JBLRemote || return 1
    LEGACY_REMOTE=$legacy_path
    legacy_locate "$SSH_STORE" JBLLocalSSH || return 1
    LEGACY_SSH=$legacy_path
    legacy_locate "$STANDARD_STORE" JBLStandard || return 1
    LEGACY_STANDARD=$legacy_path
    LEGACY_REASON=LEGACY_OWNERSHIP_OR_FILE_TYPE_MISMATCH
    component_owned "$LEGACY_REMOTE" JBL_WEB_REMOTE_REV1 &&
    component_owned "$LEGACY_SSH" JBL_LOCAL_SSH_REV1 || return 1
    # The public v0.1.0 release is rev5. It differs from rev6 only in run.sh and
    # once.sh (longer polling delays and log labels); every other file and the
    # resulting hook chain are byte-identical, so the remaining pins are shared.
    if component_owned "$LEGACY_STANDARD" JBL_STANDARD_HOOK_REV6; then
        LEGACY_STANDARD_REVISION=rev6
        legacy_run=aaa5af3e5457b715a8aad018a258fe4841f2a35076a4952b8b3993299d035b00
        legacy_once=cde5aaf6f67a7c52cc237476b141c59e8e0286405ae977f481162c145be5bace
    elif component_owned "$LEGACY_STANDARD" JBL_STANDARD_HOOK_REV5; then
        LEGACY_STANDARD_REVISION=rev5
        legacy_run=99615997df7979413ea6dd3016aef96de0388a72ee26727ebb9d7c96373a2d8f
        legacy_once=8f3c3895195ba90f57ed5650f16e82a0d83d9b4d483cbd9550484114377f4b67
    else
        return 1
    fi
    for legacy_dir in "$LEGACY_REMOTE" "$LEGACY_SSH" "$LEGACY_STANDARD"; do
        [ ! -e "$legacy_dir/TOOLKIT" ] && [ ! -L "$legacy_dir/TOOLKIT" ] || return 1
        legacy_tree "$legacy_dir" || return 1
    done
    LEGACY_REASON=LEGACY_REMOTE_IDENTITY_MISMATCH
    legacy_hash "$LEGACY_REMOTE/start.sh" 115e1bdab697a25c3d1eb8a2162369b84ebf5de035a2c738bce5e8e331ae7e19 &&
    legacy_hash "$LEGACY_REMOTE/install.sh" 5c6f7698f452671d3b098fa7df5f361ec1387a69f979b30e13d23e5381b91e5d &&
    legacy_hash "$LEGACY_REMOTE/uninstall.sh" b83c7692da5e487e795f3d86790a3ee5185dfb4c9923f44c242e2de016445163 || return 1
    LEGACY_REASON=LEGACY_SSH_IDENTITY_OR_HOST_KEY_MISMATCH
    # Prototype launcher from the device record, or the reviewed portable one.
    regular "$LEGACY_SSH/boot.sh" || return 1
    legacy_digest=$("$BB" sha256sum "$LEGACY_SSH/boot.sh") || return 1
    case "${legacy_digest%% *}" in
        eea17362b37a1a389114e583b754a4a52ba630c563b901b18ebbe8adff042472|65611c07012b0a0ff89dca5daa1b569020442cdd90691f85c40b7245cd03e43b) ;;
        *) return 1;;
    esac
    regular "$LEGACY_SSH/host_ed25519" && regular "$LEGACY_SSH/host_ed25519.pub" || return 1
    LEGACY_REASON=LEGACY_SMART_MODE_IDENTITY_MISMATCH
    legacy_hash "$LEGACY_STANDARD/run.sh" "$legacy_run" &&
    legacy_hash "$LEGACY_STANDARD/helper.sh" ca1d2dd6d7b8ccc1590946432d04334d5a8f88c2320acbae447967d0a43a8644 &&
    legacy_hash "$LEGACY_STANDARD/controller.sh" 3a166012981c6ab5bac8372e7a34e2df4a10ba6b8dc7a61ba6a68a9c07c79176 &&
    legacy_hash "$LEGACY_STANDARD/once.sh" "$legacy_once" &&
    legacy_hash "$LEGACY_STANDARD/hook.expected" 87cf62cefa6b584547f2769662295c3e84d97b30b06e8ee871e50cfceeb92581 || return 1
    LEGACY_REASON=LEGACY_HOOK_CHAIN_MISMATCH
    legacy_hash "$LEGACY_SSH/hook.before-ssh" 87cf62cefa6b584547f2769662295c3e84d97b30b06e8ee871e50cfceeb92581 &&
    legacy_hash "$LEGACY_SSH/hook.with-ssh" 5851ce93132c6c4edad67d55c4edbe06907e467bd9f76a2b600fa0191c36d5b5 &&
    legacy_hash "$LEGACY_REMOTE/hook.before-web" 5851ce93132c6c4edad67d55c4edbe06907e467bd9f76a2b600fa0191c36d5b5 &&
    legacy_hash "$LEGACY_REMOTE/hook.with-web" 5d2f06b6b73ef1ef4ae8a2363801190e768c3c4486ed81cbe88e4c267b34c15d || return 1
    if [ -e "$HOOK" ] || [ -L "$HOOK" ]; then
        legacy_hash "$HOOK" 5d2f06b6b73ef1ef4ae8a2363801190e768c3c4486ed81cbe88e4c267b34c15d || return 1
    else
        # Only our published recovery record permits resuming after hook removal.
        legacy_archive_owned || return 1
    fi
    LEGACY_REASON=PASSED
}
