"""Owner-specific archives and isolated SSH startup/hook regression fixtures."""
from pathlib import Path
import hashlib
import importlib.util
import json
import os
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import zipfile

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "tools"))
spec = importlib.util.spec_from_file_location("build_ssh", ROOT / "tools/build_ssh.py")
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)
BB = shutil.which("busybox")
assert BB, "Install BusyBox for shell fixtures"
cases = []


def passed(name):
    cases.append({"case": name})
    print("PASS", name, flush=True)


def run(argv, *, env=None):
    return subprocess.run(argv, env=env, capture_output=True, text=True, timeout=25)


def crypt_test():
    # Published-format interoperability checked against the host's openssl,
    # using disposable values passed through stdin rather than process argv.
    openssl = shutil.which("openssl")
    if openssl:
        for password in ("disposable-test-password", "unicode fixture 🦄"):
            check = subprocess.run(
                [openssl, "passwd", "-6", "-salt", "fixtureSalt", "-stdin"],
                input=password + "\n", text=True, capture_output=True, check=True,
            )
            assert builder.password_hash(password, "fixtureSalt") == check.stdout.strip()
    # Deterministic independently verified vector also covers openssl-free CI.
    assert builder.password_hash("fixture-password", "fixtureSalt") == (
        "$6$fixtureSalt$sQSjITREzEfMEXA9wpMkKTNkMHs1yQEs0cX6Kt8PuwtC4tmRgNSPG05og2XZJV3PDk4TqFPAkmRnhWHzo7dEU."
    )
    assert builder.password_hash("fixture-password") != builder.password_hash("fixture-password")
    passed("sha512crypt_interoperability_and_fresh_salts")


def archive_tests():
    with tempfile.TemporaryDirectory(prefix="jbl-public-ssh-build-") as tmp:
        folder = Path(tmp)
        password = "disposable-owner-password"
        paths = builder.build(folder / "one", "10.23.4.7", password)
        with zipfile.ZipFile(paths[0]) as archive:
            files = {name: archive.read(name) for name in archive.namelist()}
        assert files["AutoScript/AutoScript"].endswith(b"\r\n")
        assert files["JBLLocalSSH/settings.conf"] == b"ALLOWED_PC=10.23.4.7\nINTERFACE=wlan0\nAUTOSTART=1\n"
        assert not any("wifi.psk" in name or "wifi.ssid" in name for name in files)
        assert all(password.encode() not in data for data in files.values())
        assert all("host_ed25519" not in name for name in files)
        for line in files["JBLLocalSSH/checksums.txt"].decode().splitlines():
            expected, name = line.split("  ", 1)
            assert hashlib.sha256(files["JBLLocalSSH/" + name]).hexdigest() == expected
        assert all(path.stat().st_mode & 0o077 == 0 for path in paths)
        with zipfile.ZipFile(paths[1]) as archive:
            rollback = b"".join(archive.read(name) for name in archive.namelist())
            assert b"password.hash" not in rollback and b"wifi.psk" not in rollback
        passed("private_archive_integrity_no_plaintext_or_host_keys")
        ssid, passphrase = "fixture Wi-Fi", "disposable-wifi-passphrase"
        other = builder.build(folder / "two", "172.20.8.9", password, interface="ra0", autostart=False,
                              wifi=(ssid, passphrase))
        with zipfile.ZipFile(other[0]) as archive:
            settings = archive.read("JBLLocalSSH/settings.conf")
            assert b"172.20.8.9" in settings and b"INTERFACE=ra0" in settings and b"AUTOSTART=0" in settings
            encoded = archive.read("JBLLocalSSH/wifi.psk.hex").strip()
            expected = hashlib.pbkdf2_hmac("sha1", passphrase.encode(), ssid.encode(), 4096, 32).hex().encode()
            assert encoded == expected
            assert all(passphrase.encode() not in archive.read(n) for n in archive.namelist())
        passed("per_owner_lan_optional_wpa2_derivation_usb_only")
        for network in ("192.168.1.0/24", "10.0.0.0/8"):
            assert builder.allowed_ipv4(network) == network
        cidr = builder.build(folder / "cidr", "192.168.1.0/24", password)
        with zipfile.ZipFile(cidr[0]) as archive:
            assert b"ALLOWED_PC=192.168.1.0/24\n" in archive.read("JBLLocalSSH/settings.conf")
        for pc in ("8.8.8.8", "127.0.0.1", "0.0.0.0", "::1", "10.0.0.999", "10.0.0.1;echo",
                   "192.168.1.5/24", "8.8.8.0/24", "192.168.1.0/31", "192.168.1.0/32", "192.168.1.0/24;echo"):
            try:
                builder.allowed_ipv4(pc)
            except ValueError:
                continue
            raise AssertionError(pc)
        try:
            builder.build(folder / "bad", "10.23.4.7", password, interface="wlan0;echo")
        except ValueError:
            pass
        else:
            raise AssertionError("interface injection accepted")
        passed("reject_public_or_malformed_clients_and_interface_injection")
        try:
            builder.build(folder / "one", "10.23.4.7", password)
        except ValueError:
            pass
        else:
            raise AssertionError("private archive silently overwritten")
        passed("never_overwrite_existing_private_archives")


def hook_tests():
    for case in ("coexist", "missing", "changed", "symlink", "unowned", "temporary_symlink", "legacy_upgrade"):
        with tempfile.TemporaryDirectory(prefix="jbl-ssh-hook-") as tmp:
            folder = Path(tmp)
            store, hook = folder / "store", folder / "AutoScript.TSS"
            store.mkdir()
            (store / "OWNER.txt").write_text("mine\n" if case == "unowned" else "JBL_LOCAL_SSH_REV1\n")
            original = b"CLI(CLI_exec sh /var/JBLStandard/run.sh &)\r\n# preserved without final newline"
            if case != "missing":
                hook.write_bytes(original)
                hook.chmod(0o640)
            if case == "symlink":
                hook.unlink()
                hook.symlink_to(folder / "other")
            if case == "temporary_symlink":
                (folder / "sentinel").write_text("unchanged")
                (folder / "AutoScript.TSS.ssh-new").symlink_to(folder / "sentinel")
            if case == "legacy_upgrade":
                (store / "hook.before-ssh").write_bytes(original)
                (store / "hook.with-ssh").write_bytes(original + b"\r\nCLI(CLI_exec sh /var/JBLLocalSSH/boot.sh &)\r\n")
                hook.write_bytes((store / "hook.with-ssh").read_bytes())
            env = dict(os.environ, JBL_BUSYBOX=BB, JBL_SSH_STORE=str(store), JBL_SSH_HOOK=str(hook))
            command = ["/bin/sh", str(ROOT / "models/bar91/ssh/hooks.sh")]
            installed = run(command + ["install"], env=env)
            if case in ("symlink", "unowned", "temporary_symlink"):
                assert installed.returncode != 0
                if case == "temporary_symlink":
                    assert (folder / "sentinel").read_text() == "unchanged"
                if case == "symlink":
                    assert hook.is_symlink()
                passed("hook_refuse_" + case)
                continue
            assert installed.returncode == 0, installed.stderr
            expected = (b"" if case == "missing" else original) + b"\r\nCLI(CLI_exec sh /var/JBLLocalSSH/boot.sh &)\r\n"
            assert hook.read_bytes() == expected
            assert run(command + ["install"], env=env).returncode == 0
            assert hook.read_bytes() == expected
            if case == "changed":
                hook.write_bytes(expected + b"# later user customization\r\n")
                changed = hook.read_bytes()
                assert run(command + ["rollback"], env=env).returncode != 0
                assert hook.read_bytes() == changed and (store / "BOOT_DISABLED").is_file()
                assert run(command + ["install"], env=env).returncode != 0
            else:
                assert run(command + ["rollback"], env=env).returncode == 0
                if case == "missing":
                    assert not hook.exists()
                else:
                    assert hook.read_bytes() == original
                    if case == "coexist":
                        assert hook.stat().st_mode & 0o777 == 0o640
                assert (store / "BOOT_DISABLED").is_file()
                assert run(command + ["rollback"], env=env).returncode == 0
            passed("hook_" + case)


def address_tests():
    with tempfile.TemporaryDirectory(prefix="jbl-ssh-lan-") as tmp:
        folder = Path(tmp)
        wrapper = folder / "busybox"
        wrapper.write_text('''#!/bin/sh
case "$*" in
    'ip -o link show up')
        [ "${JBL_FIXTURE_LINK_FAILURE:-0}" != 1 ] || exit 1
        printf '%s\\n' "$JBL_FIXTURE_LINKS";;
    "ip "*) printf '%s\\n' "$JBL_FIXTURE_IPS";;
    *) exec ''' + BB + ''' "$@";;
esac
''')
        wrapper.chmod(0o700)
        links = "\n".join((
            "7: wlan0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500",
            "2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500",
            "1: ap0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500",
            "3: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536",
            "4: p2p0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500",
        ))
        tests = (
            ("10.23.4.7", "10.23.5.20/16", "10.23.5.20"),
            ("10.23.4.7", "10.23.5.20/24", ""),
            ("172.20.8.9", "172.20.8.11/24", "172.20.8.11"),
            ("192.168.44.7", "192.168.44.21/24", "192.168.44.21"),
            ("10.23.4.7", "10.23.4.7/24", ""),
            ("10.23.4.7", "8.8.8.8/8", ""),
            ("192.168.1.0/24", "192.168.1.109/24", "192.168.1.109"),
            ("192.168.1.0/24", "192.168.1.109/16", "192.168.1.109"),
            ("192.168.1.0/24", "192.168.2.9/24", ""),
            ("10.0.0.0/8", "10.23.5.20/24", "10.23.5.20"),
            ("192.168.1.0/24", "8.8.8.8/8", ""),
        )
        for pc, cidr, expected in tests:
            env = dict(os.environ, JBL_BUSYBOX=str(wrapper), ALLOWED_PC=pc, INTERFACE="wlan0",
                       JBL_FIXTURE_LINKS=links,
                       JBL_FIXTURE_IPS="7: wlan0 inet " + cidr + " scope global wlan0")
            result = run(["/bin/sh", "-c", '. "$1"; find_address', "fixture", str(ROOT / "models/bar91/ssh/common.sh")], env=env)
            assert result.returncode == 0 and result.stdout.strip() == expected, (pc, cidr, result.stdout, result.stderr)
        passed("actual_subnet_address_selection_excludes_public_other_lan_and_pc")
        multi = "\n".join((
            "7: wlan0 inet 192.168.1.40/24 scope global wlan0",
            "2: eth0 inet 192.168.1.20/24 scope global eth0",
            "2: eth0 inet 192.168.1.9/24 scope global eth0",
            "1: ap0 inet 192.168.1.2/24 scope global ap0",
            "3: lo inet 192.168.1.1/24 scope global lo",
            "4: p2p0 inet 192.168.1.3/24 scope global p2p0",
        ))
        for interface, addresses, expected in (
            ("auto", multi, "192.168.1.9"),
            ("auto", "\n".join(reversed(multi.splitlines())), "192.168.1.9"),
            ("wlan0", multi, "192.168.1.40"),
            ("auto", multi.splitlines()[0], "192.168.1.40"),
            ("auto", multi.splitlines()[1], "192.168.1.20"),
            ("auto", "2: eth0 inet 192.168.2.9/24 scope global eth0", ""),
            ("auto", "1: ap0 inet 192.168.1.2/24 scope global ap0", ""),
            ("auto", "", ""),
        ):
            env = dict(os.environ, JBL_BUSYBOX=str(wrapper), ALLOWED_PC="192.168.1.0/24",
                       INTERFACE=interface, JBL_FIXTURE_IPS=addresses, JBL_FIXTURE_LINKS=links)
            result = run(["/bin/sh", "-c", '. "$1"; find_address', "fixture", str(ROOT / "models/bar91/ssh/common.sh")], env=env)
            assert result.returncode == 0 and result.stdout.strip() == expected, (interface, addresses, result.stdout, result.stderr)
        passed("auto_lan_selection_wired_wifi_stable_order_hotspot_exclusion_and_explicit_override")
        disconnected = links.replace(
            "eth0: <BROADCAST,MULTICAST,UP,LOWER_UP>",
            "eth0: <NO-CARRIER,BROADCAST,MULTICAST,UP>",
        )
        for label, interface, current_links, addresses, expected in (
            ("wired_no_carrier", "auto", disconnected, multi, "192.168.1.40"),
            ("explicit_wired_no_carrier", "eth0", disconnected, multi, ""),
            ("wired_no_carrier_only", "auto", disconnected, multi.splitlines()[1], ""),
            ("wired_dormant", "auto", links.replace("eth0: <BROADCAST", "eth0: <DORMANT,BROADCAST"), multi, "192.168.1.40"),
            ("wired_admin_down", "auto", links.replace("eth0: <BROADCAST,MULTICAST,UP,LOWER_UP>", "eth0: <BROADCAST,MULTICAST,LOWER_UP>"), multi, "192.168.1.40"),
            ("missing_link_state", "auto", "", multi, ""),
            ("link_query_failed", "auto", links, multi, ""),
            ("linked_interface_name", "auto", links.replace("eth0:", "eth0@if8:"), multi.replace("eth0 inet", "eth0@if8 inet"), "192.168.1.9"),
            ("wired_reconnected", "auto", links, multi, "192.168.1.9"),
        ):
            env = dict(os.environ, JBL_BUSYBOX=str(wrapper), ALLOWED_PC="192.168.1.0/24",
                       INTERFACE=interface, JBL_FIXTURE_IPS=addresses, JBL_FIXTURE_LINKS=current_links,
                       JBL_FIXTURE_LINK_FAILURE=str(int(label == "link_query_failed")))
            result = run(["/bin/sh", "-c", '. "$1"; find_address', "fixture", str(ROOT / "models/bar91/ssh/common.sh")], env=env)
            assert result.returncode == 0 and result.stdout.strip() == expected, (label, result.stdout, result.stderr)
        passed("lan_selection_requires_operational_link_despite_retained_addresses")


def session_tests():
    # Firmware layout fixture: /bin and /sbin hold BusyBox plus applets of a
    # foreign C runtime that crash under the SSH namespace's vendor loader;
    # vendor directories hold native utilities and links into both.
    applets_wanted = ("cat", "dd", "od", "awk", "pidof", "date", "clear", "top", "ifconfig", "sort", "reboot", "uptime")
    available = set(run([BB, "--list"]).stdout.split())
    assert set(applets_wanted) <= available, sorted(set(applets_wanted) - available)
    with tempfile.TemporaryDirectory(prefix="jbl-ssh-session-") as tmp:
        root = Path(tmp).resolve()
        for name in ("bin", "sbin", "usr/bin", "usr/sbin", "usr/local/bin", "usr/local/sbin", "ram"):
            (root / name).mkdir(parents=True)
        (root / "bin/busybox").symlink_to(BB)
        toolbox = root / "bin/toolbox"
        toolbox.write_text("#!/bin/sh\necho 'Segmentation fault' >&2\nexit 139\n")
        toolbox.chmod(0o755)
        for name in ("cat", "dd", "od", "pidof", "date", "clear", "toolbox-only"):
            (root / "bin" / name).symlink_to("toolbox")
        (root / "sbin/ifconfig").symlink_to("../bin/toolbox")
        # Power commands stay with the firmware even though BusyBox has them.
        (root / "sbin/reboot").symlink_to("../bin/toolbox")
        # Hard-float program in a vendor directory (/usr/bin/awk -> gawk on the bar):
        # it names the system armhf loader and crashes with the vendor libraries.
        (root / "system/usr/bin").mkdir(parents=True)
        gawk = root / "system/usr/bin/gawk"
        gawk.write_text("#!/bin/sh\n# interpreter /lib/ld-linux-armhf.so.3\necho 'Segmentation fault' >&2\nexit 139\n")
        gawk.chmod(0o755)
        (root / "usr/bin/awk").symlink_to("../../system/usr/bin/gawk")
        (root / "usr/bin/sort").symlink_to("../../bin/busybox")
        for name, folder in (("top", "usr/bin"), ("ssh-keygen", "usr/local/bin")):
            path = root / folder / name
            path.write_text('#!/bin/sh\necho "vendor-' + name + ' $LD_LIBRARY_PATH"\n')
            path.chmod(0o755)
        # A vendor program whose bytes only resemble the loader path as a
        # regular expression ("." matching any character) stays a vendor one.
        lookalike = root / "usr/bin/uptime"
        lookalike.write_text("#!/bin/sh\n# /lib/ld-linux-armhfXsoX3\necho vendor-uptime\n")
        lookalike.chmod(0o755)
        (root / "data.txt").write_text("data-ok\n")
        applets_base = root / "ram/applets"
        source = (ROOT / "models/bar91/ssh/session.sh").read_text()
        for old, new in (
            ("BB=/bin/busybox", "BB=" + str(root / "bin/busybox")),
            ("VENDOR_DIRS='/usr/local/bin /usr/bin /usr/local/sbin /usr/sbin'",
             "VENDOR_DIRS='" + " ".join(str(root / d) for d in ("usr/local/bin", "usr/bin", "usr/local/sbin", "usr/sbin")) + "'"),
            ("FIRMWARE_DIRS='/bin /sbin'", "FIRMWARE_DIRS='" + str(root / "bin") + " " + str(root / "sbin") + "'"),
            ("APPLETS_BASE=/tmp/jbl-local-ssh-applets", "APPLETS_BASE=" + str(applets_base)),
        ):
            assert source.count(old) == 1, old
            source = source.replace(old, new)
        session = root / "session.sh"
        session.write_text(source)

        def cache_for(script):
            # The cache name carries the start of the script's SHA-256.
            return Path(str(applets_base) + "." + hashlib.sha256(script.read_bytes()).hexdigest()[:16])

        applets = cache_for(session)
        vendor_lib = "/system/bdplib:/system/usr/lib:/system/usr/local/lib"
        data = str(root / "data.txt")
        command = "; ".join((
            "cat " + data, "dd if=" + data + " 2>/dev/null", "printf A | od -An -c",
            "echo x | awk '{print \"awk-ok\"}'", "pidof no-such-process; echo pidof=$?",
            "date +%Y | grep -Eq '^[0-9]{4}$' && echo date-ok", "clear >/dev/null && echo clear-ok",
            "ifconfig lo >/dev/null 2>&1; echo ifconfig=$?", "printf 'b\\na\\n' | sort | tr -d '\\n'; echo",
            "top", "ssh-keygen", "uptime", "for c in cat awk sort top ifconfig toolbox-only reboot; do command -v $c; done",
        ))
        base = {"PATH": "/usr/bin:/bin", "HOME": str(root)}

        def session_run(extra, stdin=None):
            return subprocess.run([BB, "sh", str(session)], env=dict(base, **extra), input=stdin,
                                  capture_output=True, text=True, timeout=25)

        expected = [
            "data-ok", "data-ok", "A", "awk-ok", "pidof=1", "date-ok", "clear-ok", "ifconfig=0", "ab",
            "vendor-top " + vendor_lib, "vendor-ssh-keygen " + vendor_lib, "vendor-uptime",
            str(applets / "cat"), str(applets / "awk"), str(root / "usr/bin/sort"),
            str(root / "usr/bin/top"), str(applets / "ifconfig"), str(root / "bin/toolbox-only"),
            str(root / "sbin/reboot"),
        ]
        result = session_run({"SSH_ORIGINAL_COMMAND": command})
        lines = [line.strip() for line in result.stdout.splitlines()]
        assert result.returncode == 0 and lines == expected, (lines, result.stderr)
        assert "Segmentation fault" not in result.stderr, result.stderr
        assert applets.is_dir() and not applets.is_symlink() and applets.stat().st_mode & 0o777 == 0o700
        assert os.readlink(applets / "awk") == str(root / "bin/busybox")
        for kept in ("top", "sort", "ssh-keygen", "toolbox-only", "reboot", "uptime"):
            assert not os.path.lexists(applets / kept), kept
        assert sorted(p.name for p in (root / "ram").iterdir()) == [applets.name]
        passed("session_runs_firmware_commands_as_busybox_applets_and_keeps_vendor_utilities")

        # Later sessions reuse the per-boot cache, interactively as well.
        (applets / "reuse-marker").write_text("")
        # Distribution BusyBox builds print a banner and, depending on the
        # build, the prompt on stdout; the bar's prints neither. Mark the
        # command output so only it is compared.
        result = session_run({"PS1": ""}, stdin="echo '<'$(cat " + data + ")'>'\necho '<'$(command -v od)'>'\nexit\n")
        assert result.returncode == 0, result.stderr
        output = re.findall(r"<([^<>\n]*)>", result.stdout)
        assert output == ["data-ok", str(applets / "od")], (result.stdout, result.stderr)
        assert (applets / "reuse-marker").exists()
        passed("interactive_session_reuses_applet_cache")

        # A cache path this session did not create is never adopted; the
        # shell still starts with the vendor-first PATH.
        shutil.rmtree(applets)
        applets.write_text("foreign\n")
        result = session_run({"SSH_ORIGINAL_COMMAND": "command -v cat; ssh-keygen; echo $PATH"})
        assert result.returncode == 0, result.stderr
        assert result.stdout.splitlines()[:2] == [str(root / "bin/cat"), "vendor-ssh-keygen " + vendor_lib]
        assert str(applets) not in result.stdout.splitlines()[2]
        assert applets.read_text() == "foreign\n"
        passed("session_starts_without_foreign_applet_cache")

        # A changed script (an update without a reboot) builds its own cache
        # instead of reusing one made by older rules.
        applets.unlink()
        assert session_run({"SSH_ORIGINAL_COMMAND": "true"}).returncode == 0
        old = applets
        with session.open("a") as script:
            script.write("# changed selection rules\n")
        applets = cache_for(session)
        assert applets != old
        result = session_run({"SSH_ORIGINAL_COMMAND": "command -v cat"})
        assert result.returncode == 0 and result.stdout.split() == [str(applets / "cat")], (result.stdout, result.stderr)
        assert old.is_dir() and sorted(p.name for p in (root / "ram").iterdir()) == sorted([old.name, applets.name])
        passed("changed_session_script_builds_a_new_applet_cache")


def installer_tests():
    # Actual control flow and hooks, adapted to isolated temp paths. Only root
    # metadata, native-tool checks and live-daemon detection are fixture stubs.
    for case in ("live_upgrade", "live_network_update", "network_listener_missing", "live_other_pc", "bad_integrity", "wrong_native_build", "boot_wait_bounded"):
        with tempfile.TemporaryDirectory(prefix="jbl-ssh-install-") as tmp:
            folder = Path(tmp)
            usb, store, ram = folder / "usb", folder / "store", folder / "ram"
            for directory in (usb, store, ram):
                directory.mkdir()
            hook = folder / "AutoScript.TSS"
            hook.write_bytes(b"CLI(CLI_exec sh /var/JBLStandard/run.sh &)\r\n")
            (store / "OWNER.txt").write_text("JBL_LOCAL_SSH_REV1\n")
            (store / "sshd_config").write_text("AllowUsers root@" + ("10.23.4.8" if case == "live_other_pc" else "10.23.4.7") + "\n")
            (store / "password.hash").write_text("preserve-existing-private-hash\n")
            wrapper = folder / "busybox"
            wrapper.write_text("#!/bin/sh\ncase \"$1\" in id) echo 0;; stat) if [ \"$3\" = %u ]; then echo 0; else exec "+ BB + " \"$@\"; fi;; sleep) exit 0;; *) exec " + BB + " \"$@\";; esac\n")
            wrapper.chmod(0o700)
            replacements = {"/mnt/sda1/JBLLocalSSH": str(usb), "/var/JBLLocalSSH": str(store),
                            "/var/AutoScript.TSS": str(hook), "/tmp/jbl-local-ssh": str(ram / "lock")}

            def adapt(source):
                for index, old in enumerate(replacements):
                    source = source.replace(old, "@@SSH_PATH_" + str(index) + "@@")
                for index, new in enumerate(replacements.values()):
                    source = source.replace("@@SSH_PATH_" + str(index) + "@@", new)
                return source

            for path in (ROOT / "models/bar91/ssh").glob("*.sh"):
                (usb / path.name).write_text(adapt(path.read_text()))
            common = (usb / "common.sh").read_text()
            common += "\nverify_tools() { return " + ("1" if case == "wrong_native_build" else "0") + "; }\n"
            common += "daemon_running() { return " + ("1" if case in ("boot_wait_bounded", "network_listener_missing") else "0") + "; }\n"
            if case == "boot_wait_bounded":
                common += "find_address() { return 0; }\n"
            (usb / "common.sh").write_text(common)
            (usb / "settings.conf").write_text("ALLOWED_PC=10.23.4.7\nINTERFACE=wlan0\nAUTOSTART=1\n")
            payload = {p.name: p.read_bytes() for p in usb.iterdir()}
            (usb / "checksums.txt").write_bytes(builder.manifest(payload))
            if case == "bad_integrity":
                (usb / "session.sh").write_text("changed\n")
            env = dict(os.environ, JBL_BUSYBOX=str(wrapper), JBL_SSH_STORE=str(store), JBL_SSH_HOOK=str(hook))
            if case == "boot_wait_bounded":
                for path in usb.iterdir():
                    shutil.copyfile(path, store / path.name)
                for name in ("passwd", "nsswitch.conf", "ld.so.preload", "host_ed25519", "host_ed25519.pub"):
                    (store / name).write_text("disposable-fixture\n")
                result = run(["/bin/sh", str(store / "boot.sh")], env=env)
                status = (store / "BOOT_STATUS.txt").read_text().strip()
                assert status == "NO_ALLOWED_LAN_WITHIN_180_SECONDS", (status, result.stderr)
            else:
                args = ["/bin/sh", str(usb / "start.sh")]
                if case in ("live_network_update", "network_listener_missing"):
                    args.append("--managed-network-update")
                result = run(args, env=env)
                status = (usb / "STATUS.txt").read_text().strip()
                expected = {"live_upgrade": "ALREADY_RUNNING_PASSWORD_UNCHANGED", "live_network_update":"ALREADY_RUNNING_PASSWORD_UNCHANGED", "network_listener_missing":"SSH_LISTENER_CHANGED_NO_RESTART", "live_other_pc": "RUNNING_ALLOWED_PC_DIFFERS",
                            "bad_integrity": "USB_INTEGRITY_FAILED", "wrong_native_build": "UNSUPPORTED_NATIVE_SSH_BUILD"}[case]
                assert status == expected, (status, result.stderr, (usb / "setup.log").read_text())
                assert (store / "password.hash").read_text() == "preserve-existing-private-hash\n"
                if case == "live_upgrade":
                    assert (store / "boot.sh").exists() and b"/boot.sh &)" in hook.read_bytes()
                    for name in ("namespace.sh", "session.sh"):
                        assert (store / name).read_bytes() == (usb / name).read_bytes()
                elif case == "live_network_update":
                    assert b"/JBLLocalSSH/boot.sh" not in hook.read_bytes()
                    for name in ("namespace.sh", "session.sh"):
                        assert (store / name).read_bytes() == (usb / name).read_bytes()
                else:
                    assert b"/JBLLocalSSH/boot.sh" not in hook.read_bytes()
            passed("installer_" + case)


def follow_tests():
    # The actual boot helper against fixture addresses and a stand-in daemon.
    # Only the namespace launch, listener confirmation and daemon identity are
    # stubs; selection changes, locking, statuses and stop rules are real.
    def state(pid):
        try:
            return Path("/proc/" + str(pid) + "/stat").read_text().rsplit(") ", 1)[1].split()[0]
        except (OSError, IndexError):
            return ""

    def alive(pid):
        return state(pid) not in ("", "Z")

    def until(condition, label):
        deadline = time.monotonic() + 20
        while not condition():
            assert time.monotonic() < deadline, label
            time.sleep(0.02)

    for case in ("moved", "disabled", "replaced", "retried", "abandoned", "disabled_during_move"):
        with tempfile.TemporaryDirectory(prefix="jbl-ssh-follow-") as tmp:
            folder = Path(tmp)
            store, ram = folder / "store", folder / "ram"
            store.mkdir()
            ram.mkdir()
            polls, daemons, releases = folder / "polls", folder / "daemons", folder / "releases"
            wrapper = folder / "busybox"
            # Every lock release records the stand-in daemons still running then.
            wrapper.write_text('''#!/bin/sh
case "$1" in
    sleep) echo >> "''' + str(polls) + '''"; exec sleep 0.05;;
    unshare)
        echo "$$" >> "''' + str(daemons) + '''"
        echo "$$" > "$JBL_SSH_STORE/sshd.pid"
        exec sleep 60;;
    rmdir)
        for pid in $(''' + BB + ''' cat "''' + str(daemons) + '''" 2>/dev/null); do
            state=$(''' + BB + ''' awk '{sub(/^.*\\) /, ""); print $1}' "/proc/$pid/stat" 2>/dev/null)
            case "$state" in ''|Z) ;; *) echo "$pid" >> "''' + str(releases) + '''";; esac
        done
        exec ''' + BB + ''' "$@";;
    *) exec ''' + BB + ''' "$@";;
esac
''')
            wrapper.chmod(0o700)
            for name in ("boot.sh", "common.sh"):
                source = (ROOT / "models/bar91/ssh" / name).read_text()
                source = source.replace("/var/JBLLocalSSH", str(store)).replace("/tmp/jbl-local-ssh", str(ram / "lock"))
                (store / name).write_text(source)
            with (store / "common.sh").open("a") as common:
                common.write('''
verify_tools() { return 0; }
prepare_privsep() { return 0; }
find_address() { "$BB" cat "$STORE/fixture-address"; }
daemon_running() {
    pid=$("$BB" cat "$STORE/sshd.pid" 2>/dev/null) && [ -n "$pid" ] &&
        [ -n "$("$BB" tr '\\000' ' ' < "/proc/$pid/cmdline" 2>/dev/null)" ]
}
wait_listener() {
    refusals=$("$BB" cat "$STORE/fixture-refusals")
    if [ "$refusals" -gt 0 ]; then
        echo $((refusals - 1)) > "$STORE/fixture-refusals"
        # Refuse only once the stand-in daemon has started, as a real listener
        # has before its confirmation fails. Refusing at once let the stop race
        # the daemon's startup, so the recorded daemon count varied.
        waited=0
        while [ "$("$BB" cat "$STORE/sshd.pid" 2>/dev/null)" != "$1" ]; do
            [ "$waited" -lt 100 ] || return 1
            "$BB" sleep 1
            waited=$((waited + 1))
        done
        return 1
    fi
    confirmed=0
    while [ "$("$BB" cat "$STORE/sshd.pid" 2>/dev/null)" != "$1" ]; do
        [ "$confirmed" -lt 100 ] || return 1
        "$BB" sleep 1
        confirmed=$((confirmed + 1))
    done
    [ ! -e "$STORE/fixture-disable" ] || "$BB" cp "$STORE/OWNER.txt" "$STORE/BOOT_DISABLED"
}
''')
            (store / "OWNER.txt").write_text("JBL_LOCAL_SSH_REV1\n")
            (store / "settings.conf").write_text("ALLOWED_PC=192.168.1.0/24\nINTERFACE=auto\nAUTOSTART=1\n")
            (store / "sshd_config").write_text("Port 2222\nListenAddress 192.168.1.40\nUseDNS no\n")
            (store / "host_fingerprint.txt").write_text("256 SHA256:disposable-fixture root@fixture (ED25519)\n")
            (store / "fixture-refusals").write_text("0\n")
            for name in ("namespace.sh", "session.sh", "passwd", "nsswitch.conf", "ld.so.preload", "host_ed25519", "host_ed25519.pub"):
                (store / name).write_text("disposable-fixture\n")
            read = lambda name: (store / name).read_text().strip()
            daemon = lambda: int(read("sshd.pid"))
            poll_count = lambda: polls.stat().st_size if polls.exists() else 0
            released_with = lambda: [int(pid) for pid in releases.read_text().split()]

            def idle():
                seen = poll_count()
                time.sleep(0.5)
                return poll_count() == seen

            def polled(count):
                target = poll_count() + count
                until(lambda: poll_count() >= target, "follower stopped polling")

            replacement = None
            try:
                (store / "fixture-address").write_text("192.168.1.40\n")
                env = dict(os.environ, JBL_BUSYBOX=str(wrapper), JBL_SSH_STORE=str(store))
                result = run(["/bin/sh", str(store / "boot.sh")], env=env)
                assert result.returncode == 0 and read("BOOT_STATUS.txt") == "SSH_READY", (result.stderr, read("boot.log"))
                first = daemon()
                assert alive(first) and read("listen-address") == "192.168.1.40"
                assert not (ram / "lock-boot-lock").exists()
                if case == "moved":
                    (store / "fixture-address").write_text("192.168.1.9\n")
                    until(lambda: "FOLLOW=LISTENER_MOVED" in read("boot.log"), read("boot.log"))
                    second = daemon()
                    assert second != first and alive(second) and not alive(first)
                    assert read("listen-address") == "192.168.1.9" and read("BOOT_STATUS.txt") == "SSH_READY"
                    assert read("sshd_config").splitlines() == ["Port 2222", "ListenAddress 192.168.1.9", "UseDNS no"]
                    assert "IP=192.168.1.9" in read("connection.txt").splitlines()
                    assert released_with() == [first, second] and not (ram / "lock-boot-lock").exists()
                    passed("follow_moves_listener_when_selected_lan_address_changes")
                    # No LAN is not a reason to drop the listener: its address may return.
                    (store / "fixture-address").write_text("")
                    polled(6)
                    assert daemon() == second and alive(second) and read("listen-address") == "192.168.1.9"
                    passed("follow_keeps_listener_while_no_lan_address_is_selected")
                    os.kill(second, signal.SIGTERM)
                    until(idle, "follower outlived a stopped listener")
                    (store / "fixture-address").write_text("192.168.1.40\n")
                    time.sleep(0.5)
                    assert daemon() == second and not alive(second) and read("listen-address") == "192.168.1.9"
                    passed("follow_never_restarts_a_stopped_listener")
                elif case in ("disabled", "replaced"):
                    if case == "disabled":
                        (store / "BOOT_DISABLED").write_text("JBL_LOCAL_SSH_REV1\n")
                    else:
                        replacement = subprocess.Popen(["sleep", "60"])
                        (store / "sshd.pid").write_text(str(replacement.pid) + "\n")
                    until(idle, "follower outlived a " + case + " listener")
                    (store / "fixture-address").write_text("192.168.1.9\n")
                    time.sleep(0.5)
                    assert alive(first) and read("listen-address") == "192.168.1.40"
                    assert len(daemons.read_text().split()) == 1
                    passed("follow_leaves_" + case + "_listener_alone")
                elif case == "disabled_during_move":
                    # Setup disables the service while the new listener starts.
                    (store / "fixture-disable").write_text("1\n")
                    (store / "fixture-address").write_text("192.168.1.9\n")
                    until(lambda: "FOLLOW=DISABLED" in read("boot.log"), read("boot.log"))
                    until(idle, "follower outlived a disabled move")
                    started = [int(pid) for pid in daemons.read_text().split()]
                    assert len(started) == 2 and not any(alive(pid) for pid in started)
                    assert released_with() == [first] and not (ram / "lock-boot-lock").exists()
                    passed("follow_stops_new_listener_before_unlocking_a_disabled_move")
                else:
                    (store / "fixture-refusals").write_text("1\n" if case == "retried" else "9\n")
                    (store / "fixture-address").write_text("192.168.1.9\n")
                    if case == "retried":
                        until(lambda: "FOLLOW=LISTENER_MOVED" in read("boot.log"), read("boot.log"))
                        assert read("boot.log").count("FOLLOW=SSH_LISTENER_NOT_CONFIRMED") == 1
                        started = [int(pid) for pid in daemons.read_text().split()]
                        assert len(started) == 3 and daemon() == started[2] and alive(started[2])
                        assert not alive(started[0]) and not alive(started[1])
                        assert read("listen-address") == "192.168.1.9" and read("BOOT_STATUS.txt") == "SSH_READY"
                        # The unconfirmed daemon was gone before its lock release.
                        assert released_with() == [started[0], started[2]]
                        passed("follow_retries_after_an_unconfirmed_listener")
                    else:
                        until(lambda: read("fixture-refusals") == "6", read("boot.log"))
                        until(idle, "follower kept retrying")
                        assert read("boot.log").count("FOLLOW=SSH_LISTENER_NOT_CONFIRMED") == 3
                        assert read("fixture-refusals") == "6" and read("BOOT_STATUS.txt") == "SSH_LISTENER_NOT_CONFIRMED"
                        started = [int(pid) for pid in daemons.read_text().split()]
                        assert len(started) == 4 and not any(alive(pid) for pid in started)
                        assert released_with() == [first] and not (ram / "lock-boot-lock").exists()
                        passed("follow_stops_after_three_unconfirmed_listeners")
            finally:
                (store / "BOOT_DISABLED").write_text("JBL_LOCAL_SSH_REV1\n")
                for pid in daemons.read_text().split() if daemons.exists() else ():
                    if alive(pid):
                        os.kill(int(pid), signal.SIGTERM)
                if replacement is not None:
                    replacement.kill()
                    replacement.wait()
                until(idle, "follower still running")


crypt_test()
archive_tests()
hook_tests()
address_tests()
session_tests()
installer_tests()
follow_tests()
for script in (ROOT / "models/bar91/ssh").glob("*.sh"):
    result = run([BB, "sh", "-n", str(script)])
    assert result.returncode == 0, (script.name, result.stderr)
source = b"\n".join(p.read_bytes() for p in (ROOT / "models/bar91/ssh").glob("*.sh"))
assert not re.search(rb"\$6\$[./0-9A-Za-z]{1,16}\$[./0-9A-Za-z]{86}", source)
assert b"BEGIN OPENSSH PRIVATE KEY" not in source and b"\x00" not in source
passed("shell_syntax_and_no_embedded_password_hashes_keys_or_binaries")
out = ROOT / "build/test-results"
out.mkdir(parents=True, exist_ok=True)
(out / "ssh-tests.json").write_text(json.dumps({
    "scope": "Host fixtures: per-owner builder, integrity, CIDR selection, SSH session command resolution, actual hook transaction/rollback, active installation upgrade, bounded boot failure and listener moves after LAN address changes with a stand-in daemon. Native binaries and physical reboot not exercised.",
    "cases": cases,
}, indent=2) + "\n")
