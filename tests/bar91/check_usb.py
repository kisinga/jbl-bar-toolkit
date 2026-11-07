"""Unified USB packaging, parser, transactions and recovery in isolated host fixtures."""
from pathlib import Path
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import zipfile

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'tools'))
import build_usb
import build_ssh
import prepare_usb

BB = shutil.which('busybox')
assert BB
CONFIG = (ROOT / 'models/bar91/usb/config.example.txt').read_bytes()
cases = []


def passed(name):
    cases.append({'case': name})
    print('PASS', name, flush=True)


def changed(**values):
    text = CONFIG.decode()
    for key, value in values.items():
        old = next(line for line in text.splitlines() if line.startswith(key + '='))
        text = text.replace(old, key + '=' + value)
    return text.encode()


for label, data in {
    'unknown_key': CONFIG + b'execute=anything\n',
    'duplicate_key': CONFIG + b'ssh=false\n',
    'shell_injection': changed(web_port='80; touch /tmp/jbl-parser-injection'),
    'command_substitution': changed(allowed_cidr='$(id)'),
    'public_network': changed(allowed_cidr='8.8.8.0/24'),
    'host_bits': changed(allowed_cidr='192.168.1.4/24'),
    'broad_network': changed(allowed_cidr='10.0.0.0/8'),
    'ambiguous_octet': changed(allowed_cidr='192.168.001.0/24'),
    'ssh_port_collision': changed(web_port='2222'),
    'bad_boolean': changed(auto_standard='yes'),
    'memory_without_auto_standard': changed(auto_standard='false') + b'smart_mode_memory=true\n',
    'bad_memory_boolean': CONFIG + b'smart_mode_memory=yes\n',
    'duplicate_memory': CONFIG + b'smart_mode_memory=true\nsmart_mode_memory=false\n',
    'unknown_action': changed(action='upgrade'),
    'unsupported_format': changed(format='2'),
    'missing_key': CONFIG.replace(b'ssh=true\n', b''),
    'nul': CONFIG + b'\0',
    'oversized': CONFIG + b'\n' * 8192,
}.items():
    try:
        build_usb.parse_config(data)
    except ValueError:
        passed('reject_' + label)
    else:
        raise AssertionError(label)
for subnet in ('10.23.0.0/16', '172.20.8.0/24', '192.168.1.0/30'):
    assert build_usb.parse_config(changed(allowed_cidr=subnet))['ALLOWED_CIDR'] == subnet
assert build_usb.parse_config(CONFIG.replace(b'\n', b'\r\n'))['SSH'] == 'true'
assert build_usb.parse_config(CONFIG)['SMART_MODE_MEMORY'] == 'false'
assert build_usb.parse_config(CONFIG + b'smart_mode_memory=true\n')['SMART_MODE_MEMORY'] == 'true'
passed('optional_smart_mode_memory')
passed('canonical_private_subnets_and_windows_line_endings')

(ROOT / 'dist').mkdir(exist_ok=True)
with tempfile.TemporaryDirectory(prefix='usb-tests-', dir=ROOT / 'dist') as output:
    output = Path(output)
    install, recovery = build_usb.build(output / 'package', CONFIG, password='disposable-toolkit-password',
                                        wifi=('fixture Wi-Fi', 'disposable-wifi-passphrase'))
    with zipfile.ZipFile(install) as archive:
        package = {n: archive.read(n) for n in archive.namelist()}
    with zipfile.ZipFile(recovery) as archive:
        rescue = {n: archive.read(n) for n in archive.namelist()}
    assert package['AutoScript/AutoScript'].endswith(b'\r\n')
    assert package['JBLToolkit/files/remote'][:4] == b'\x7fELF'
    assert package['JBLToolkit/files/remote'][18:20] == b'\x28\x00'
    for line in package['JBLToolkit/SHA256SUMS'].decode().splitlines():
        expected, name = line.split('  ', 1)
        assert hashlib.sha256(package['JBLToolkit/' + name]).hexdigest() == expected
    assert b'config.txt' not in package['JBLToolkit/SHA256SUMS']
    for data in package.values():
        assert b'disposable-toolkit-password' not in data and b'disposable-wifi-passphrase' not in data
    assert not any('password' in n or 'wifi.' in n or n.endswith('/remote') for n in rescue)
    assert install.stat().st_mode & 0o077 == recovery.stat().st_mode & 0o077 == 0
    passed('real_arm_build_private_credentials_integrity_and_credential_free_recovery')
    validated = prepare_usb.package_files(install)
    with tempfile.TemporaryDirectory(prefix='jbl-stage-') as temp:
        target = Path(temp)
        prepare_usb.stage_files(target, validated)
        prepare_usb.verify_files(target, validated)
        try:
            prepare_usb.stage_files(target, validated)
        except ValueError:
            pass
        else:
            raise AssertionError('must not overwrite an occupied USB')
        (target / 'JBLToolkit/config.txt').write_bytes(changed(action='check'))
        prepare_usb.verify_files(target, validated)
        (target / 'JBLToolkit/files/remote').write_bytes(b'corruption')
        try:
            prepare_usb.verify_files(target, validated)
        except ValueError:
            pass
        else:
            raise AssertionError('readback must detect changed binary')
    passed('usb_staging_readback_editable_config_and_overwrite_guard')
    for bad_name in ('../escape', '/absolute', 'JBLToolkit/../escape', 'jbltoolkit/launch.sh'):
        bad_zip = output / 'unsafe.zip'
        with zipfile.ZipFile(bad_zip, 'w') as archive:
            for name, data in package.items():
                archive.writestr(name, data)
            archive.writestr(bad_name, b'unsafe')
        try:
            prepare_usb.package_files(bad_zip)
        except ValueError:
            pass
        else:
            raise AssertionError(bad_name)
    passed('usb_rejects_traversal_absolute_and_case_collision_archives')
    private_hash = output / 'password.hash'
    private_hash.write_bytes(package['JBLToolkit/files/ssh/password.hash'])
    for label, contents, password in (
        ('malformed_hash', 'not-a-crypt-hash', None),
        ('ambiguous_credentials', private_hash.read_text(), 'disposable-password'),
    ):
        private_hash.write_text(contents)
        try:
            build_usb.build(output / label, CONFIG, password=password, ssh_password_hash_file=private_hash)
        except ValueError:
            passed('builder_reject_' + label)
        else:
            raise AssertionError(label)
    private_hash.write_bytes(package['JBLToolkit/files/ssh/password.hash'])
    hashed_package, _ = build_usb.build(output / 'hash-reuse', changed(action='check'), ssh_password_hash_file=private_hash)
    with zipfile.ZipFile(hashed_package) as archive:
        assert archive.read('JBLToolkit/files/ssh/password.hash') == private_hash.read_bytes()
    passed('builder_reuses_private_hash_for_check_without_password_rotation')
    for label, folder, config, password in (
        ('overwrite', output / 'package', CONFIG, 'disposable-toolkit-password'),
        ('missing_credentials', output / 'missing', CONFIG, None),
        ('wrong_action', output / 'action', changed(action='configure'), None),
        ('public_output', Path('/tmp/jbl-forbidden-toolkit-output'), CONFIG, 'disposable-toolkit-password'),
    ):
        try:
            build_usb.build(folder, config, password=password)
        except ValueError:
            passed('builder_reject_' + label)
        else:
            raise AssertionError(label)

    host_remote = output / 'host-remote'
    subprocess.run(['go', 'build', '-o', str(host_remote), '.'], cwd=ROOT / 'apps/remote', check=True)

    # A tiny host executable exercises actual startup, PID identity, readiness and
    # graceful shutdown. Native SSH/network/firmware operations are fixture stubs.
    native_source = output / 'fixture.go'
    native_source.write_text('''package main
import ("flag"; "fmt"; "os"; "os/exec"; "os/signal"; "strconv"; "syscall")
func main() {
 flag.String("config", "", ""); ready:=flag.String("ready-file", "", "")
 publish:=flag.Bool("publish-directory",false,""); inspect:=flag.Bool("inspect-device",false,""); flag.Bool("require-awake",false,""); flag.Parse()
 if *publish { cmd:=exec.Command(os.Getenv("JBL_FIXTURE_HOST_REMOTE"),append([]string{"-publish-directory"},flag.Args()...)...); cmd.Stdout=os.Stdout; cmd.Stderr=os.Stderr; if cmd.Run()!=nil { os.Exit(1) }; return }
 if *inspect {fmt.Println(`{"schema":1,"supported":true,"power":"On","mode":"Standard"}`); return}
 c:=make(chan os.Signal,1); signal.Notify(c,syscall.SIGTERM,syscall.SIGINT)
 if *ready!="" {os.WriteFile(*ready, []byte(strconv.Itoa(os.Getpid())+"\\n"),0600)}
 <-c
 if *ready!="" {os.Remove(*ready)}
}
''')
    native_binary = output / 'fixture-remote'
    subprocess.run(['go', 'build', '-o', str(native_binary), str(native_source)], check=True)

    def scenario(label, *, config=None, hook_present=True, existing=None, fail=None, migration=None):
        with tempfile.TemporaryDirectory(prefix='jbl-toolkit-fixture-') as temp:
            root = Path(temp)
            usb, store = root / 'usb', root / 'var/JBLToolkit'
            ssh, remote, hook = root / 'var/JBLLocalSSH', root / 'var/JBLRemote', root / 'var/AutoScript.TSS'
            hook.parent.mkdir()
            original = b'CLI(CLI_exec sh /var/custom/start.sh &)\r\n# no trailing newline'
            if hook_present:
                hook.write_bytes(original)
                hook.chmod(0o640)
            for name, data in package.items():
                if not name.startswith('JBLToolkit/'):
                    continue
                path = usb / name.removeprefix('JBLToolkit/')
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(data)
            paths = {
                '/mnt/sda1/JBLToolkit': str(usb), '/var/JBLToolkit': str(store),
                '/var/JBLLocalSSH': str(ssh), '/var/JBLRemote': str(remote),
                '/var/JBLStandard': str(root / 'var/JBLStandard'), '/var/AutoScript.TSS': str(hook),
                '/tmp/jbl-standard-mutation-active': str(root / 'legacy-mutation'),
                '/tmp/jbl-standard-hook-active': str(root / 'legacy-hook-active'),
                '/tmp/jbl-standard-watch-active': str(root / 'legacy-watch-active'),
                '/tmp/jbl-local-ssh-boot-lock': str(root / 'legacy-ssh-boot'),
                '/tmp/jbl-local-ssh-setup-lock': str(root / 'legacy-ssh-setup'),
                '/tmp/jbl-web-start-lock': str(root / 'legacy-web-start'),
            }
            for script in usb.rglob('*.sh'):
                text = script.read_text()
                for index, old in enumerate(paths):
                    text = text.replace(old, '@@PATH_' + str(index) + '@@')
                for index, new in enumerate(paths.values()):
                    text = text.replace('@@PATH_' + str(index) + '@@', new)
                script.write_text(text)
            (usb / 'preflight.sh').write_text('preflight() { PREFLIGHT_REASON=' + ('FIXTURE_FAILURE; return 1' if fail == 'preflight' else 'PASSED; return 0') + '; }\n' +
                'preflight_all() { preflight; fixture_result=$?; probe_native awake; PREFLIGHT_NATIVE_RESULT=passed; printf "FIXTURE=isolated-native-boundary\\n" > "$USB/PREFLIGHT.txt"; return "$fixture_result"; }\n')
            (usb / 'files/remote').write_bytes(native_binary.read_bytes())
            # Provisioning is unrelated to the host LAN; never run native Wi-Fi.
            (usb / 'files/ssh/wifi.sh').write_text('exit 0\n')
            (usb / 'files/ssh/start.sh').write_text('''#!/bin/sh
case "$1" in --managed|--managed-network-update) ;; *) exit 1;; esac
[ "$1" != --managed-network-update ] || [ ! -e "$JBL_SSH_STORE/BOOT_DISABLED" ] || exit 1
''' + ('exit 1\n' if fail == 'ssh' else f'''
printf 'SSH_READY\\n' > "{ssh}/STATUS.txt"
printf '#!/bin/sh\\nexit 0\\n' > "{ssh}/boot.sh"
[ -f "{ssh}/host_ed25519.pub" ] || printf 'fixture-public-host-key\\n' > "{ssh}/host_ed25519.pub"
printf 'fixture-connection\\n' > "{ssh}/connection.txt"
exit 0
'''))
            immutable_names = [line.split('  ', 1)[1] for line in (usb / 'SHA256SUMS').read_text().splitlines()]
            if label == 'credentials_in_config':
                for name in ('files/ssh/password.hash', 'files/ssh/wifi.ssid.hex', 'files/ssh/wifi.psk.hex'):
                    (usb / name).unlink()
                    immutable_names.remove(name)
            if migration == 'missing_credentials':
                (usb / 'files/ssh/password.hash').unlink()
                immutable_names.remove('files/ssh/password.hash')
            def manifest():
                (usb / 'SHA256SUMS').write_bytes(build_ssh.manifest({n: (usb / n).read_bytes() for n in immutable_names}))
            manifest()
            (usb / 'config.txt').write_bytes(config or changed(ssh='false'))
            if fail == 'integrity':
                (usb / 'files/remote').write_bytes(b'corrupted')
            if existing == 'ssh':
                ssh.mkdir(); (ssh / 'OWNER.txt').write_text('JBL_LOCAL_SSH_REV1\n')
            if existing == 'hook_symlink':
                hook.unlink(); hook.symlink_to(root / 'absent')
            env = dict(os.environ, JBL_TOOLKIT_USB=str(usb), JBL_TOOLKIT_STORE=str(store),
                       JBL_SSH_STORE=str(ssh), JBL_REMOTE_STORE=str(remote), JBL_TOOLKIT_HOOK=str(hook),
                       JBL_TOOLKIT_LOCK=str(root / 'lock'), JBL_BUSYBOX=BB, JBL_FIXTURE_HOST_REMOTE=str(host_remote),
                       JBL_LEGACY_STORE=str(root / 'var/JBLToolkitLegacy'), JBL_STANDARD_STORE=str(root / 'var/JBLStandard'))
            if fail in ('publish', 'publish_race'):
                with (usb / 'common.sh').open('a') as stream:
                    if fail == 'publish_race':
                        stream.write('\npublish_directory() { mkdir "$2"; echo preserve > "$2/sentinel"; "$JBL_FIXTURE_HOST_REMOTE" -publish-directory "$1" "$2"; }\n')
                    else:
                        stream.write('\npublish_directory() { return 1; }\n')
                manifest()
            if migration:
                standard = root / 'var/JBLStandard'
                archive = root / 'var/JBLToolkitLegacy'
                remote.mkdir(); ssh.mkdir()
                shutil.copytree(ROOT / 'models/bar91/smart-mode/JBLInstall/payload', standard)
                fixture = ROOT / 'tests/bar91/fixtures/legacy'
                for name in ('start.sh', 'install.sh', 'uninstall.sh'):
                    (remote / name).write_bytes((fixture / ('remote-' + name)).read_bytes())
                (remote / 'OWNER.txt').write_text('JBL_WEB_REMOTE_REV1\n')
                (ssh / 'OWNER.txt').write_text('JBL_LOCAL_SSH_REV1\n')
                (ssh / 'boot.sh').write_bytes((fixture / 'ssh-boot.sh').read_bytes())
                (ssh / 'host_ed25519').write_bytes(b'fixture-private-host-key\n')
                (ssh / 'host_ed25519.pub').write_bytes(b'fixture-existing-public-host-key\n')
                base = (standard / 'hook.expected').read_bytes()
                ssh_hook = base + b'CLI(CLI_exec sh /var/JBLLocalSSH/boot.sh &)\r\n'
                web_hook = ssh_hook + b'\r\nCLI(CLI_exec sh /var/JBLRemote/start.sh &)\r\n'
                (ssh / 'hook.before-ssh').write_bytes(base)
                (ssh / 'hook.with-ssh').write_bytes(ssh_hook)
                (remote / 'hook.before-web').write_bytes(ssh_hook)
                (remote / 'hook.with-web').write_bytes(web_hook)
                hook.write_bytes(web_hook)
                if migration == 'changed_script':
                    with (remote / 'start.sh').open('a') as stream: stream.write('\n# external modification\n')
                if migration == 'changed_hook': hook.write_bytes(web_hook + b'# external change\n')
                if migration == 'symlink': (remote / 'foreign-link').symlink_to(root / 'unrelated')
                if migration == 'resume':
                    archive.mkdir()
                    (archive / 'OWNER.txt').write_text('JBL_TOOLKIT_LEGACY_REV1\n')
                    (archive / 'hook.legacy').write_bytes(web_hook)
                    (archive / 'hook.mode').write_text('600\n')
                    remote.rename(archive / 'JBLRemote')
                    hook.unlink()
                if migration == 'live_remote':
                    (remote / 'remote').write_bytes(native_binary.read_bytes())
                    (remote / 'remote').chmod(0o700)
                    (remote / 'config.json').write_text('{}')
                    legacy_process = subprocess.Popen([str(remote / 'remote'), '-config', str(remote / 'config.json')])
                    (remote / 'remote.pid').write_text(str(legacy_process.pid))
                    time.sleep(0.1)
                # Actual unrelated PID refusal is exercised before hook removal.
                if migration == 'unrelated_pid':
                    unrelated_migration = subprocess.Popen(['sleep', '60'])
                    (remote / 'remote.pid').write_text(str(unrelated_migration.pid))
            network_update = False
            def launch():
                result = subprocess.run(['/bin/sh', str(usb / ('ssh-update.sh' if network_update else 'launch.sh'))], env=env,
                                        capture_output=True, text=True, timeout=70)
                status = (usb / 'STATUS.txt').read_text().strip()
                return result, status
            try:
                if label.startswith('ssh_reject_'):
                    env['JBL_TOOLKIT_TRANSPORT'] = 'ssh'
                result, status = launch()
                if label.startswith('ssh_reject_'):
                    assert result.returncode != 0 and status == 'SSH_UPDATE_REQUIRES_MANAGED_INSTALLATION', status
                    assert not store.exists() and not remote.exists()
                    assert hook.read_bytes() == original
                    passed(label)
                    return
                if migration:
                    if migration == 'missing_credentials':
                        assert result.returncode != 0 and status == 'MIGRATION_SSH_CREDENTIALS_REQUIRED_NO_CHANGES', status
                        assert not store.exists() and not archive.exists() and remote.exists() and ssh.exists() and standard.exists()
                        assert hook.read_bytes() == web_hook and not (remote / 'DISABLED').exists()
                    elif migration in ('changed_script', 'changed_hook', 'symlink'):
                        assert result.returncode != 0 and status == 'MIGRATION_IDENTITY_FAILED_NO_CHANGES', (status, (usb / 'setup.log').read_text())
                        assert not store.exists() and not archive.exists() and remote.exists() and ssh.exists() and standard.exists()
                        assert not (remote / 'DISABLED').exists()
                        assert hook.read_bytes() == (web_hook + b'# external change\n' if migration == 'changed_hook' else web_hook)
                    elif migration == 'unrelated_pid':
                        assert status == 'MIGRATION_REMOTE_STOP_FAILED_REBOOT' and unrelated_migration.poll() is None, status
                        assert hook.read_bytes() == web_hook and remote.exists() and not store.exists()
                        unrelated_migration.terminate(); unrelated_migration.wait(timeout=10)
                    else:
                        assert result.returncode == 0 and status == 'INSTALLED_PENDING_REBOOT_TEST', (status, (usb / 'setup.log').read_text())
                        assert (archive / 'RETIRED').exists() and not standard.exists()
                        if migration == 'live_remote':
                            assert legacy_process.wait(timeout=10) == 0
                        assert (archive / 'hook.legacy').read_bytes() == web_hook
                        assert (archive / 'JBLRemote/start.sh').read_bytes() == (fixture / 'remote-start.sh').read_bytes()
                        assert (ssh / 'host_ed25519').read_bytes() == b'fixture-private-host-key\n'
                        assert (ssh / 'host_ed25519.pub').read_bytes() == b'fixture-existing-public-host-key\n'
                        assert hook.read_bytes() == (store / 'hook.managed').read_bytes() and (store / 'hook.absent').exists()
                        assert b'JBLStandard' not in hook.read_bytes()
                        # Explicit migration reinsertion is passive after installation.
                        pid = (remote / 'remote.pid').read_bytes()
                        assert launch()[1] == 'ALREADY_INSTALLED_LOGS_EXPORTED'
                        assert (remote / 'remote.pid').read_bytes() == pid
                        (usb / 'RECOVER').touch()
                        assert launch()[1] == 'RECOVERED_REBOOT_REQUIRED'
                        assert not hook.exists() and (archive / 'RETIRED').exists()
                    passed(label)
                    return
                if build_usb.parse_config((usb / 'config.txt').read_bytes())['ACTION'] == 'check':
                    assert status == ('CHECK_BLOCKED_READ_CHECKS' if existing or fail else 'CHECK_PASSED_READY_FOR_INSTALL_OR_CONFIGURE'), status
                    assert not store.exists() and not remote.exists()
                    assert hook.read_bytes() == original
                    assert ssh.exists() == bool(existing)
                    assert 'DEVICE_SERVICES_CHANGED=false' in (usb / 'CHECKS.txt').read_text()
                    assert (usb / 'DIAGNOSTICS.txt').is_file() and (usb / 'native-check.json').is_file()
                    if fail:
                        assert 'NATIVE_RESULT=passed_independent_of_failed_preflight' in (usb / 'CHECKS.txt').read_text()
                    passed(label)
                    return
                expected = {
                    'integrity': 'PACKAGE_INTEGRITY_FAILED',
                    'preflight': 'PREFLIGHT_FAILED_CHECK_FIRMWARE_STORAGE_AND_NATIVE_SSH',
                    'ssh': 'SSH_INSTALL_FAILED',
                    'publish': 'STORE_PUBLISH_FAILED',
                    'publish_race': 'STORE_PUBLISH_FAILED',
                }.get(fail)
                if existing:
                    expected = {'ssh': 'EXISTING_COMPONENT_REMOVE_WITH_ORIGINAL_INSTALLER',
                                'hook_symlink': 'STARTUP_HOOK_UNSAFE'}[existing]
                if expected:
                    assert result.returncode != 0 and status == expected, (status, (usb / 'setup.log').read_text())
                    if existing != 'hook_symlink':
                        assert (hook.read_bytes() == original) if hook_present else not hook.exists()
                    if fail == 'publish_race':
                        assert (store / 'sentinel').read_text().strip() == 'preserve'
                        assert not list(store.parent.glob('JBLToolkit.stage.*'))
                    elif fail == 'ssh':
                        assert (store / 'DISABLED').exists()
                    else:
                        assert not store.exists()
                    passed(label)
                    return
                assert result.returncode == 0 and status == 'INSTALLED_PENDING_REBOOT_TEST', (status, (usb / 'setup.log').read_text())
                managed = hook.read_bytes()
                assert managed.count(b'/boot.sh &)') == 1
                assert managed.startswith(original if hook_present else b'')
                assert not (store / 'DISABLED').exists()
                if label == 'credentials_in_config':
                    expected_credentials = build_usb.parse_config(config)
                    assert (store / 'ssh-input/password.hash').read_text().strip() == expected_credentials['SSH_PASSWORD_HASH']
                    assert not (store / 'ssh-input/wifi.psk.hex').exists()
                    assert not (usb / 'logs/config.txt').exists()
                    assert expected_credentials['SSH_PASSWORD_HASH'] not in (usb / 'setup.log').read_text()
                if label == 'automatic_clean_update':
                    assert (store / 'package.manifest').read_bytes() == (usb / 'SHA256SUMS').read_bytes()
                    pid = (remote / 'remote.pid').read_bytes()
                    assert launch()[1] == 'ALREADY_INSTALLED_LOGS_EXPORTED'
                    assert (remote / 'remote.pid').read_bytes() == pid
                    (usb / 'RECOVER').touch()
                    assert launch()[1] == 'RECOVERED_REBOOT_REQUIRED'
                    assert hook.read_bytes() == original
                    passed(label)
                    return
                if label.startswith(('usb_update_', 'ssh_update_')):
                    update_label = label.replace('ssh_update_', 'usb_update_')
                    old_config = (store / 'config.txt').read_bytes()
                    old_pid = (remote / 'remote.pid').read_bytes()
                    old_password = (store / 'ssh-input/password.hash').read_bytes()
                    (ssh / 'host_ed25519').write_bytes(b'original-private-host-key')
                    original_keys = {n:(ssh / n).read_bytes() for n in ('host_ed25519','host_ed25519.pub')}
                    (remote / 'bass-offset.json').write_text('{"offset_db":-1.5}')
                    (remote / 'front-eq.json').write_text('{"gains_db":[-1,0,0,0,0]}')
                    (usb / 'VERSION.txt').write_text('next-build\n')
                    with (usb / 'files/remote').open('ab') as stream:
                        stream.write(b'next-build')
                    with (usb / 'boot.sh').open('a') as stream:
                        stream.write('\n# new manager revision\n')
                    # A public update needs no credentials or chosen feature flags:
                    # preserve all installed settings, credentials and tuning.
                    for name in ('files/ssh/password.hash','files/ssh/wifi.ssid.hex','files/ssh/wifi.psk.hex'):
                        (usb / name).unlink()
                        immutable_names.remove(name)
                    (usb / 'config.txt').write_bytes(changed(action='update',ssh='false',web_remote='false',auto_standard='false'))
                    if update_label == 'usb_update_bad_credentials':
                        (store / 'ssh-input/password.hash').write_text('invalid-hash')
                    if update_label == 'usb_update_runtime_corrupt':
                        with (remote / 'start.sh').open('a') as stream: stream.write('\n# corrupt\n')
                    if update_label == 'usb_update_hook_changed':
                        hook.write_bytes(managed + b'# foreign hook\n')
                    if update_label == 'usb_update_start_failure':
                        (usb / 'files/remote-start.sh').write_text('exit 1\n')
                    if update_label == 'usb_update_preflight_failure':
                        (usb / 'preflight.sh').write_text('preflight() { PREFLIGHT_REASON=FIXTURE_FAILURE; return 1; }\n')
                    if label.startswith('ssh_update_'):
                        network_update = True
                        with (usb / 'files/ssh/common.sh').open('a') as stream:
                            stream.write('\ndaemon_running() { return ' + ('1' if label == 'ssh_update_listener_missing' else '0') + '; }\n')
                        # The outer hardware dispatcher is a separate fixture boundary.
                        (usb / 'dispatch.sh').write_text('exec /bin/sh \"$JBL_TOOLKIT_USB/launch.sh\"\n')
                    manifest()
                    if update_label == 'usb_update_bad_usb':
                        (usb / 'files/remote').write_bytes(b'corrupt USB')
                    result, status = launch()
                    if network_update:
                        assert (usb / 'SSH-EXIT.txt').read_text().strip() == str(result.returncode)
                    expected_failures = {
                        'usb_update_bad_credentials':'UPDATE_SSH_CREDENTIALS_INVALID_NO_CHANGES',
                        'usb_update_runtime_corrupt':'UPDATE_RUNTIME_INTEGRITY_FAILED_NO_CHANGES',
                        'usb_update_hook_changed':'STARTUP_HOOK_CHANGED_PRESERVED',
                        'usb_update_preflight_failure':'PREFLIGHT_FAILED_CHECK_FIRMWARE_STORAGE_AND_NATIVE_SSH',
                        'usb_update_bad_usb':'PACKAGE_INTEGRITY_FAILED',
                        'usb_update_start_failure':'REMOTE_START_FAILED',
                        'usb_update_listener_missing':'SSH_LISTENER_CHANGED_NO_CHANGES',
                    }
                    if update_label in expected_failures:
                        assert result.returncode != 0 and status == expected_failures[update_label], (status,(usb / 'setup.log').read_text())
                        if update_label == 'usb_update_start_failure':
                            assert (store / 'DISABLED').exists() and hook.read_bytes() == original
                        else:
                            assert (remote / 'remote.pid').read_bytes() == old_pid
                            assert not (store / 'DISABLED').exists()
                    else:
                        assert result.returncode == 0 and status == 'INSTALLED_PENDING_REBOOT_TEST', (status,(usb / 'setup.log').read_text())
                        assert (store / 'package.manifest').read_bytes() == (usb / 'SHA256SUMS').read_bytes()
                        assert (store / 'config.txt').read_bytes() == old_config
                        assert (store / 'ssh-input/password.hash').read_bytes() == old_password
                        assert (remote / 'remote').read_bytes() == (usb / 'files/remote').read_bytes()
                        assert (store / 'boot.sh').read_bytes() == (usb / 'boot.sh').read_bytes()
                        assert (remote / 'bass-offset.json').read_text() == '{"offset_db":-1.5}'
                        assert (remote / 'front-eq.json').read_text() == '{"gains_db":[-1,0,0,0,0]}'
                        assert all((ssh / n).read_bytes() == v for n,v in original_keys.items())
                        assert hook.read_bytes() == managed and not (store / 'DISABLED').exists()
                        assert launch()[1] == 'ALREADY_INSTALLED_LOGS_EXPORTED'
                        (usb / 'RECOVER').touch()
                        assert launch()[1] == 'RECOVERED_REBOOT_REQUIRED'
                        assert hook.read_bytes() == original
                    passed(label)
                    return
                # Qualification cannot pass on the installation's own kernel boot.
                installed_config = (usb / 'config.txt').read_bytes()
                (usb / 'config.txt').write_bytes(installed_config.replace(b'action=install', b'action=verify'))
                result, status = launch()
                assert result.returncode != 0 and status == 'VERIFICATION_INCOMPLETE_READ_VERIFY'
                assert 'FAIL=NEW_KERNEL_BOOT_NOT_YET_PROVEN' in (usb / 'VERIFY.txt').read_text()
                (usb / 'config.txt').write_bytes(installed_config)
                if label == 'new_boot_and_runtime_integrity':
                    # Real boot dispatcher and actual remote fixture, no SSH or native controls.
                    subprocess.run(['/bin/sh', str(store / 'boot.sh')], env=env, check=True, timeout=20)
                    assert (store / 'BOOT_STATUS.txt').read_text().strip() == 'BOOT_SERVICES_READY'
                    assert (store / 'boot.id').read_text() == Path('/proc/sys/kernel/random/boot_id').read_text()
                    (store / 'install.boot_id').write_text('previous-kernel-boot\n')
                    # Native read-only snapshot is the explicit firmware fixture boundary.
                    with (usb / 'common.sh').open('a') as stream:
                        stream.write('\nprobe_native() { printf \'{"power":"On","mode":"Standard"}\\n\' > "$USB/native-check.json"; }\n')
                    manifest()
                    (store / 'package.manifest').write_bytes((usb / 'SHA256SUMS').read_bytes())
                    (usb / 'config.txt').write_bytes(installed_config.replace(b'action=install', b'action=verify'))
                    result, status = launch()
                    assert result.returncode == 0 and status == 'VERIFIED_CURRENT_BOOT_PENDING_USER_TESTS', (usb / 'VERIFY.txt').read_text()
                    with (remote / 'start.sh').open('a') as stream:
                        stream.write('\n# damaged installation\n')
                    assert launch()[1] == 'VERIFICATION_INCOMPLETE_READ_VERIFY'
                    assert 'FAIL=RUNTIME_INTEGRITY' in (usb / 'VERIFY.txt').read_text()
                    subprocess.run(['/bin/sh', str(store / 'boot.sh')], env=env, timeout=20)
                    assert (store / 'BOOT_STATUS.txt').read_text().strip() == 'BOOT_RUNTIME_INTEGRITY_FAILED'
                    (usb / 'RECOVER').touch()
                    assert launch()[1] == 'RECOVERED_REBOOT_REQUIRED'
                    passed(label)
                    return
                if build_usb.parse_config((usb / 'config.txt').read_bytes())['AUTO_STANDARD'] == 'true':
                    assert json.loads((remote / 'config.json').read_text())['auto_standard']
                if label == 'automation_without_web_or_ssh':
                    assert json.loads((remote / 'config.json').read_text())['headless']
                    assert not ssh.exists()
                # Reinsertion does not replay/restart work or apply edited settings.
                pid_before = (remote / 'remote.pid').read_bytes() if remote.exists() else None
                (usb / 'config.txt').write_bytes(changed(ssh='false', web_remote='false', auto_standard='false'))
                result, status = launch()
                assert result.returncode == 0 and status == 'ALREADY_INSTALLED_LOGS_EXPORTED'
                assert hook.read_bytes() == managed
                if pid_before:
                    assert (remote / 'remote.pid').read_bytes() == pid_before
                if label == 'different_package_refused_without_service_changes':
                    (usb / 'VERSION.txt').write_text('different package\n')
                    manifest()
                    result, status = launch()
                    assert result.returncode != 0 and status == 'PACKAGE_VERSION_CONFLICT_USE_ORIGINAL_USB'
                    assert (remote / 'remote.pid').read_bytes() == pid_before
                    (usb / 'RECOVER').touch()
                    assert launch()[1] == 'RECOVERED_REBOOT_REQUIRED'
                elif label.startswith('changed_hook_'):
                    hook.write_bytes(managed + b'\r\n# external change\r\n')
                    (usb / 'RECOVER').touch()
                    result, status = launch()
                    assert status == 'RECOVERY_INCOMPLETE_READ_LOG' and result.returncode != 0
                    assert hook.read_bytes().endswith(b'# external change\r\n')
                    assert (store / 'DISABLED').exists()
                elif label == 'emergency_recovery_ignores_broken_config_and_payload':
                    (usb / 'config.txt').write_text('broken')
                    (usb / 'files/remote').write_text('broken')
                    (usb / 'RECOVER').touch()
                    result, status = launch()
                    assert result.returncode == 0 and status == 'RECOVERED_REBOOT_REQUIRED'
                    assert hook.read_bytes() == original
                else:
                    # Explicit configure can disable every feature and later re-enable.
                    (usb / 'config.txt').write_bytes(changed(action='configure', ssh='false', web_remote='false', auto_standard='false'))
                    result, status = launch()
                    assert result.returncode == 0, (status, (usb / 'setup.log').read_text())
                    assert not (remote / 'remote.pid').exists()
                    (usb / 'config.txt').write_bytes(config or changed(action='configure', ssh='false'))
                    text = (usb / 'config.txt').read_text().replace('action=install', 'action=configure')
                    (usb / 'config.txt').write_text(text)
                    result, status = launch()
                    assert result.returncode == 0, (status, (usb / 'setup.log').read_text())
                    # Independent recovery ZIP scripts can undo the installation.
                    for name in ('launch.sh', 'common.sh', 'recover.sh'):
                        assert (usb / name).exists()
                    (usb / 'RECOVER').touch()
                    result, status = launch()
                    assert result.returncode == 0 and status == 'RECOVERED_REBOOT_REQUIRED', (status, (usb / 'setup.log').read_text())
                    assert hook.read_bytes() == original if hook_present else not hook.exists()
                    if hook_present:
                        assert hook.stat().st_mode & 0o777 == 0o640
                    assert store.exists() and (store / 'DISABLED').exists()
                    # Recovery is repeatable.
                    assert launch()[1] == 'RECOVERED_REBOOT_REQUIRED'
                passed(label)
            finally:
                if (remote / 'remote.pid').exists():
                    pid = int((remote / 'remote.pid').read_text())
                    try:
                        os.kill(pid, 15)
                    except ProcessLookupError:
                        pass

    for migration in ('complete', 'resume', 'live_remote', 'changed_script', 'changed_hook', 'symlink', 'unrelated_pid', 'missing_credentials'):
        scenario('legacy_migration_' + migration, config=changed(action='migrate'), migration=migration)
    scenario('automatic_legacy_update', config=changed(action='update'), migration='complete')
    scenario('automatic_clean_update', config=changed(action='update',ssh='false'))
    for update in ('complete','runtime_corrupt','hook_changed','bad_usb','bad_credentials','preflight_failure','start_failure'):
        scenario('usb_update_' + update, config=changed(ssh='true'))
    for update in ('complete','runtime_corrupt','hook_changed','bad_usb','bad_credentials','preflight_failure','start_failure','listener_missing'):
        scenario('ssh_update_' + update, config=changed(ssh='true'))
    for action in ('install','update','configure'):
        scenario('ssh_reject_' + action, config=changed(action=action,ssh='false'))
    scenario('install_configure_reinsert_recover_original_hook')
    scenario('check_does_not_install', config=changed(action='check', ssh='false'))
    scenario('check_collects_native_after_preflight_failure', config=changed(action='check', ssh='false'), fail='preflight')
    scenario('check_preserves_standalone_install', config=changed(action='check', ssh='false'), existing='ssh')
    scenario('new_boot_and_runtime_integrity')
    scenario('install_recover_originally_absent_hook', hook_present=False)
    scenario('automation_without_web_or_ssh', config=changed(ssh='false', web_remote='false'))
    scenario('all_features_ssh_uses_managed_install', config=CONFIG)
    scenario('credentials_in_config', config=CONFIG + ('ssh_password_hash=' + build_ssh.password_hash('fixture-config-password', 'fixturesalt') + '\nwifi_ssid_hex=66697874757265\nwifi_psk_hex=' + 'a'*64 + '\n').encode())
    scenario('ssh_only', config=changed(web_remote='false', auto_standard='false'))
    scenario('web_only', config=changed(ssh='false', auto_standard='false'))
    scenario('all_features_initially_disabled', config=changed(ssh='false', web_remote='false', auto_standard='false'))
    scenario('different_package_refused_without_service_changes')
    scenario('changed_hook_recovery_preserves_external_bytes')
    scenario('changed_hook_originally_absent_preserves_external_bytes', hook_present=False)
    scenario('emergency_recovery_ignores_broken_config_and_payload')
    scenario('integrity_failure_before_mutation', fail='integrity')
    scenario('preflight_failure_before_mutation', fail='preflight')
    scenario('ssh_failure_disables_partial_install', fail='ssh', config=CONFIG)
    scenario('standalone_install_preserved', existing='ssh')
    scenario('unsafe_hook_preserved', existing='hook_symlink')
    scenario('atomic_manager_publish_failure_preserves_original_hook', fail='publish')
    scenario('atomic_manager_publish_race_preserves_external_store', fail='publish_race')

    # Real Go automation-only startup must work even with an invalid listen
    # address, proving this execution path never attempts to open HTTP.
    headless_config, receipt = output / 'headless.json', output / 'READY'
    headless_config.write_text(json.dumps({'headless': True, 'auto_standard': True,
                                          'allowed_cidr': '192.168.1.0/24', 'listen': 'invalid/listener'}))
    process = subprocess.Popen([str(host_remote), '-config', str(headless_config), '-ready-file', str(receipt)],
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    try:
        deadline = time.monotonic() + 15
        while not receipt.exists() and process.poll() is None and time.monotonic() < deadline:
            time.sleep(0.025)
        assert receipt.read_text().strip() == str(process.pid)
        time.sleep(0.1)
        assert process.poll() is None
        process.terminate()
        _, stderr = process.communicate(timeout=20)
        assert process.returncode == 0, stderr.decode()
        assert not receipt.exists()
        passed('real_go_headless_no_http_and_graceful_shutdown')
    finally:
        if process.poll() is None:
            process.terminate()
            process.wait(timeout=20)
    headless_config.write_text(json.dumps({'headless': True, 'auto_standard': False,
                                          'allowed_cidr': '192.168.1.0/24'}))
    result = subprocess.run([str(host_remote), '-config', str(headless_config), '-ready-file', str(receipt)],
                            capture_output=True, timeout=10)
    assert result.returncode != 0 and not receipt.exists()
    passed('real_go_reject_inert_headless_configuration')
    result = subprocess.run([str(host_remote), '-inspect-device', '-require-awake', '-config', '/nonexistent/config',
                             '-ready-file', str(receipt)], capture_output=True, timeout=20)
    report = json.loads(result.stdout)
    assert result.returncode != 0 and report['schema'] == 1 and not report['supported'] and report['error']
    assert report['boot_id'] == Path('/proc/sys/kernel/random/boot_id').read_text().strip()
    assert isinstance(report['kernel_filesystems'], list) and not receipt.exists()
    passed('real_go_inspector_is_independent_of_server_config_and_never_ready')

    # Exercise the actual SSH installer in live-daemon fixtures. The managed
    # path must skip hooks, and standalone use must preserve a toolkit store.
    for managed in (True, False):
        with tempfile.TemporaryDirectory(prefix='jbl-managed-ssh-') as temp:
            folder = Path(temp); usb = folder / 'input'; ssh = folder / 'store'; hook = folder / 'hook'
            usb.mkdir(); ssh.mkdir(); hook.write_text('preserve this hook')
            for path in (ROOT / 'models/bar91/ssh').glob('*.sh'):
                (usb / path.name).write_bytes(path.read_bytes())
            with (usb / 'common.sh').open('a') as stream:
                stream.write('\nverify_tools() { return 0; }\ndaemon_running() { return 0; }\n')
            (usb / 'settings.conf').write_text('ALLOWED_PC=192.168.1.0/24\nINTERFACE=wlan0\nAUTOSTART=1\n')
            (usb / 'checksums.txt').write_bytes(build_ssh.manifest({p.name:p.read_bytes() for p in usb.iterdir()}))
            (ssh / 'OWNER.txt').write_text('JBL_LOCAL_SSH_REV1\n')
            (ssh / 'TOOLKIT').write_text('JBL_TOOLKIT_REV1\n')
            (ssh / 'sshd_config').write_text('AllowUsers root@192.168.1.0/24\n')
            wrapper = folder / 'busybox'
            wrapper.write_text('#!/bin/sh\ncase "$1" in id) echo 0;; stat) if [ "$2" = -c ] && [ "$3" = %u ]; then echo 0; else exec ' + BB + ' "$@"; fi;; *) exec ' + BB + ' "$@";; esac\n')
            wrapper.chmod(0o700)
            env = dict(os.environ, JBL_SSH_USB=str(usb), JBL_SSH_STORE=str(ssh), JBL_SSH_HOOK=str(hook), JBL_BUSYBOX=str(wrapper))
            command = ['/bin/sh', str(usb / 'start.sh')] + (['--managed'] if managed else [])
            result = subprocess.run(command, env=env, capture_output=True, timeout=20)
            assert hook.read_text() == 'preserve this hook'
            status = (usb / 'STATUS.txt').read_text().strip()
            assert status == ('ALREADY_RUNNING_PASSWORD_UNCHANGED' if managed else 'MANAGED_BY_TOOLKIT_USE_TOOLKIT_USB'), status
            assert result.returncode == (0 if managed else 1)
            passed('real_ssh_managed_skips_hook' if managed else 'real_ssh_standalone_preserves_toolkit')

    with tempfile.TemporaryDirectory(prefix='jbl-pid-ownership-') as temp:
        folder = Path(temp); remote = folder / 'remote'; remote.mkdir()
        (remote / 'OWNER.txt').write_text('JBL_WEB_REMOTE_REV1\n')
        (remote / 'TOOLKIT').write_text('JBL_TOOLKIT_REV1\n')
        unrelated = subprocess.Popen(['sleep', '60'])
        try:
            (remote / 'remote.pid').write_text(str(unrelated.pid))
            env = dict(os.environ, JBL_REMOTE_STORE=str(remote), JBL_BUSYBOX=BB)
            result = subprocess.run(['/bin/sh', '-c', '. "$1"; stop_remote', 'fixture', str(ROOT / 'models/bar91/usb/common.sh')],
                                    env=env, capture_output=True, timeout=10)
            assert result.returncode != 0 and unrelated.poll() is None
            passed('unrelated_live_pid_is_never_signalled')
        finally:
            unrelated.terminate(); unrelated.wait(timeout=10)

# File-byte estimates must be independent of FAT cluster allocation and must
# retain the reserve, rounding and unreadable/empty input refusals.
with tempfile.TemporaryDirectory(prefix='jbl-space-budget-') as temp:
    folder = Path(temp)
    files = folder / 'files'; files.mkdir()
    (files / 'remote').write_bytes(b'x' * 6750356)
    (files / 'small').write_bytes(b'x' * 29243)
    command = '. "$1"; payload_size_kib "$USB/files"'
    env = dict(os.environ, BB=BB, USB=str(folder), LOCK=str(folder))
    result = subprocess.run(['/bin/sh', '-c', command, 'fixture', str(ROOT / 'models/bar91/usb/preflight.sh')], env=env, capture_output=True, check=True)
    budget = int(result.stdout)
    assert budget == sum((p.stat().st_size + 4095)//4096*4 for p in files.iterdir())
    assert budget + 2048 < 8856
    (files / 'remote').unlink(); (files / 'small').unlink()
    assert subprocess.run(['/bin/sh', '-c', command, 'fixture', str(ROOT / 'models/bar91/usb/preflight.sh')], env=env, capture_output=True).returncode != 0
    passed('native_space_budget_uses_file_bytes_not_fat_allocation')

# Reproduce the returned USB's 8,888 KiB estimate against 8,848 KiB free.
# Identical SSH staging files are reused by atomic_copy, without reducing the
# reserve. Modified, symlinked or unowned files must never receive this credit.
for kind in ('identical', 'changed', 'symlink', 'unowned', 'still_full'):
    with tempfile.TemporaryDirectory(prefix='jbl-support-space-') as temp:
        folder = Path(temp)
        usb, store, remote = folder/'usb', folder/'store', folder/'remote'
        (usb/'files/ssh').mkdir(parents=True)
        (store/'ssh-input').mkdir(parents=True)
        (store/'OWNER.txt').write_text('JBL_TOOLKIT_REV1\n' if kind != 'unowned' else 'unknown\n')
        (usb/'files/remote').write_bytes(b'x' * (6788 * 1024))
        for i in range(13):
            source = usb/'files/ssh'/f'file{i}.sh'
            source.write_bytes(b'unchanged script\n')
            target = store/'ssh-input'/source.name
            if kind == 'symlink': target.symlink_to(source)
            else: target.write_bytes(source.read_bytes() if kind != 'changed' else b'modified\n')
        wrapper = folder/'busybox'
        free = 8835 if kind == 'still_full' else 8848
        wrapper.write_text('#!/bin/sh\nif [ "$1" = df ]; then echo "Filesystem 1024-blocks Used Available Capacity Mounted"; echo "ubi 34976 26128 '+str(free)+' 75% /var"; else exec '+BB+' "$@"; fi\n')
        wrapper.chmod(0o700)
        command = '. "$1"; . "$2"; preflight_space'
        env = dict(os.environ, JBL_BUSYBOX=str(wrapper), JBL_TOOLKIT_USB=str(usb),
                   JBL_TOOLKIT_STORE=str(store), JBL_REMOTE_STORE=str(remote), JBL_TOOLKIT_LOCK=str(folder))
        result = subprocess.run(['/bin/sh','-c',command,'fixture',str(ROOT/'models/bar91/usb/common.sh'),str(ROOT/'models/bar91/usb/preflight.sh')], env=env, capture_output=True, check=False)
        assert (result.returncode == 0) == (kind == 'identical'), (kind,result.stdout,result.stderr)
        assert not result.stderr, result.stderr
        expected = 8836 if kind in ('identical','still_full') else 8888
        assert f'SPACE_REQUIRED_KIB={expected}' in result.stdout.decode(), result.stdout
        passed('support_file_space_' + kind)

# Native preflight with synthetic mounts/process identities and a command wrapper.
# No global /var, networking or vendor executable is accessed by these cases.
for label in ('supported', 'wrong_var', 'read_only', 'nested_mount', 'wrong_firmware',
              'wrong_cli', 'multiple_processes', 'no_space', 'missing_applet', 'unsupported_ssh',
              'usb_read_only', 'usb_exfat', 'native_probe_failure', 'atomic_io_failure', 'no_boot_id', 'firmware_mv_without_T', 'publish_unsupported', 'usb_block_alias', 'usb_wrong_partition', 'usb_wrong_disk', 'observed_device_space', 'managed_reuse_space', 'unmanaged_no_credit'):
    with tempfile.TemporaryDirectory(prefix='jbl-preflight-') as temp:
        folder = Path(temp); ubi = folder / 'ubi'; var = ubi / 'var'; var.mkdir(parents=True)
        vlink = folder / 'var'; vlink.symlink_to(var if label != 'wrong_var' else folder)
        mounts = folder / 'mounts'
        mounts.write_text('ubi0:ubi_boot ' + str(ubi) + ' ubifs ' + ('ro' if label == 'read_only' else 'rw') + ' 0 0\n')
        with mounts.open('a') as stream:
            device = {'usb_block_alias':'/dev/block/sda1', 'usb_wrong_partition':'/dev/block/sda2', 'usb_wrong_disk':'/dev/block/sdb1'}.get(label, '/dev/sda1')
            stream.write(device + ' /mnt/sda1 ' + ('exfat' if label == 'usb_exfat' else 'vfat') + ' ' + ('ro' if label == 'usb_read_only' else 'rw') + ' 0 0\n')
        if label == 'nested_mount':
            with mounts.open('a') as stream:
                stream.write('tmpfs ' + str(var) + ' tmpfs rw 0 0\n')
        wrapper = folder / 'busybox'
        wrapper.write_text('''#!/bin/sh
case "$1" in
 id) echo 0;;
 mv) if [ "$JBL_PREFLIGHT_CASE" = firmware_mv_without_T ]; then for arg in "$@"; do case "$arg" in -T|-nT) exit 1;; esac; done; fi; exec ''' + BB + ''' "$@";;
 cp) [ "$JBL_PREFLIGHT_CASE" != atomic_io_failure ] || exit 1; exec ''' + BB + ''' "$@";;
 --list) ''' + BB + ''' --list | ''' + BB + ''' awk -v kind="$JBL_PREFLIGHT_CASE" '!(kind=="missing_applet" && $0=="mktemp")';;
 pidof) if [ "$JBL_PREFLIGHT_CASE" = multiple_processes ]; then echo '123 456'; else echo 123; fi;;
 sha256sum)
   case "$2" in
     /proc/123/exe) if [ "$JBL_PREFLIGHT_CASE" = wrong_firmware ]; then echo 'bad  exe'; else echo '86ec83472a10dd4278285123f8ecbfb96bdd97444dd98c3a56d5d87441c90b63  exe'; fi;;
     /bin/cli) if [ "$JBL_PREFLIGHT_CASE" = wrong_cli ]; then echo 'bad  cli'; else echo '8910fb852d2f994a519615fede404fbb036e3a8f5264cc69cdff57038af3c78f  cli'; fi;;
     *) exit 1;;
   esac;;
 stat) if [ "$2" = -c ] && [ "$3" = %s ]; then
   case "$JBL_PREFLIGHT_CASE" in
     observed_device_space) echo 6492160;;
     managed_reuse_space|unmanaged_no_credit) case "$4" in */files/remote) echo 6144000;; *) echo 348160;; esac;;
     *) echo 2097152;;
   esac
   else exec ' + BB + ' "$@"; fi;;
 df) echo 'Filesystem 1024-blocks Used Available Capacity Mounted'; if [ "$JBL_PREFLIGHT_CASE" = no_space ]; then echo 'ubi 32768 32768 0 100% /var'; elif [ "$JBL_PREFLIGHT_CASE" = observed_device_space ]; then echo 'ubi 34976 19924 13224 60% /var'; elif [ "$JBL_PREFLIGHT_CASE" = managed_reuse_space ] || [ "$JBL_PREFLIGHT_CASE" = unmanaged_no_credit ]; then echo 'ubi 34976 30000 4096 90% /var'; else echo 'ubi 32768 0 32768 0% /var'; fi;;
 *) exec ''' + BB + ''' "$@";;
esac
''')
        wrapper.chmod(0o700)
        source = (ROOT / 'models/bar91/usb/preflight.sh').read_text()
        # Replace longer paths before /var to avoid recursively rewriting paths.
        for old, new in {'/mnt/ubi_boot/var': '@@VAR@@', '/mnt/ubi_boot': '@@UBI@@', '/proc/mounts': '@@MOUNTS@@', '/var': '@@LINK@@'}.items():
            source = source.replace(old, new)
        for old, new in {'@@VAR@@':str(var), '@@UBI@@':str(ubi), '@@MOUNTS@@':str(mounts), '@@LINK@@':str(vlink)}.items():
            source = source.replace(old, new)
        (folder / 'preflight.sh').write_text(source)
        (folder / 'files/ssh').mkdir(parents=True)
        (folder / 'files/ssh/common.sh').write_text('verify_tools() { return 1; }\n')
        remote_store = folder / 'remote-store'
        if label in ('managed_reuse_space', 'unmanaged_no_credit'):
            remote_store.mkdir()
            (remote_store / 'remote').write_bytes(b'unchanged remote')
            (folder / 'files/remote').write_bytes(b'unchanged remote')
            if label == 'managed_reuse_space':
                (remote_store / 'TOOLKIT').write_text('JBL_TOOLKIT_REV1\n')
        command = '. "$1"; regular() { [ -f "$1" ] && [ ! -L "$1" ]; }; directory() { [ -d "$1" ] && [ ! -L "$1" ]; }; owned() { return 1; }; boot_id() { [ "$JBL_PREFLIGHT_CASE" != no_boot_id ] && echo fixture-boot; }; probe_native() { [ "$JBL_PREFLIGHT_CASE" != native_probe_failure ]; }; publish_directory() { [ "$JBL_PREFLIGHT_CASE" != publish_unsupported ] && [ ! -e "$2" ] && "$BB" mv "$1" "$2"; }; preflight'
        env = dict(os.environ, BB=str(wrapper), USB=str(folder), SSH='true' if label=='unsupported_ssh' else 'false',
                   LOCK=str(folder), SSH_STORE=str(folder / 'ssh'), REMOTE_STORE=str(remote_store), WEB_REMOTE='true', AUTO_STANDARD='true', JBL_PREFLIGHT_CASE=label)
        result = subprocess.run(['/bin/sh', '-c', command, 'fixture', str(folder / 'preflight.sh')],
                                env=env, capture_output=True, timeout=15)
        assert (result.returncode == 0) == (label in ('supported', 'firmware_mv_without_T', 'usb_block_alias', 'observed_device_space', 'managed_reuse_space')), (label, result.stderr.decode())
        if label == 'observed_device_space': assert 'SPACE_REQUIRED_KIB=8388' in result.stdout.decode()
        if label == 'managed_reuse_space': assert 'SPACE_REQUIRED_KIB=2388' in result.stdout.decode()
        passed('native_preflight_' + label)
        matrix_command = command.removesuffix('preflight') + 'preflight_all'
        matrix = subprocess.run(['/bin/sh', '-c', matrix_command, 'fixture', str(folder / 'preflight.sh')],
                                env=env, capture_output=True, timeout=15)
        assert (matrix.returncode == 0) == (label in ('supported', 'firmware_mv_without_T', 'usb_block_alias', 'observed_device_space', 'managed_reuse_space')), (label, matrix.stderr.decode())
        report = (folder / 'PREFLIGHT.txt').read_text()
        assert len(report.splitlines()) == 10, (label, report)
        if label in ('usb_wrong_partition', 'wrong_firmware', 'unsupported_ssh', 'no_space'):
            assert 'PASS=native' in report, report
        if label == 'wrong_var':
            assert 'SKIP=atomic_io:PREREQUISITES_FAILED' in report and 'PASS=native' in report, report
        if label == 'publish_unsupported':
            assert 'FAIL=atomic_io:PERSISTENT_STORAGE_ATOMIC_NO_REPLACE_UNAVAILABLE' in report, report
        passed('independent_prerequisites_' + label)


with tempfile.TemporaryDirectory(prefix='jbl-atomic-reuse-') as temp:
    folder = Path(temp); source = folder / 'source'; target = folder / 'target'
    source.write_bytes(b'unchanged runtime'); target.write_bytes(source.read_bytes())
    wrapper = folder / 'busybox'
    wrapper.write_text('#!/bin/sh\n[ "$1" != cp ] || exit 1\nexec ' + BB + ' "$@"\n')
    wrapper.chmod(0o700)
    env = dict(os.environ, JBL_BUSYBOX=str(wrapper))
    command = '. "$1"; atomic_copy "$2" "$3"'
    args = ['/bin/sh', '-c', command, 'fixture', str(ROOT / 'models/bar91/usb/common.sh'), str(source), str(target)]
    assert subprocess.run(args, env=env, capture_output=True).returncode == 0
    source.write_bytes(b'changed runtime')
    assert subprocess.run(args, env=env, capture_output=True).returncode != 0
    assert target.read_bytes() == b'unchanged runtime'
    source.write_bytes(target.read_bytes()); (folder / 'target.new').write_bytes(b'interrupted')
    assert subprocess.run(args, env=env, capture_output=True).returncode != 0
    passed('same_runtime_reuse_survives_no_copy_space_and_preserves_conflicts')


# Network staging replaces only the removable-media prerequisite. It must stay
# private, canonical and attached to an existing managed installation.
for mode in ('valid', 'permissions', 'symlink', 'bad_name', 'not_owned'):
    with tempfile.TemporaryDirectory(prefix='jbl-network-stage-') as temp:
        folder = Path(temp)
        stage = folder / ('jbl-toolkit-ssh.' + ('a' * 32 if mode != 'bad_name' else 'bad'))
        stage.mkdir(mode=0o700)
        payload = stage / 'JBLToolkit'
        payload.mkdir(mode=0o700)
        if mode == 'permissions': stage.chmod(0o755)
        if mode == 'symlink':
            payload.rmdir()
            real = folder / 'elsewhere'; real.mkdir(mode=0o700)
            payload.symlink_to(real, target_is_directory=True)
        wrapper = folder / 'busybox'
        wrapper.write_text('#!/bin/sh\n'
            + '[ "$1:$2:$3" != "stat:-c:%u" ] || { echo 0; exit; }\n'
            + '[ "$1:$2:$3" != "readlink:-f:/tmp" ] || { echo "' + str(folder) + '"; exit; }\n'
            + 'exec ' + BB + ' "$@"\n')
        wrapper.chmod(0o700)
        script = folder / 'preflight.sh'
        script.write_text((ROOT / 'models/bar91/usb/preflight.sh').read_text().replace('/tmp/jbl-toolkit-ssh.', str(folder) + '/jbl-toolkit-ssh.'))
        command = '. "$1"; directory() { [ -d "$1" ] && [ ! -L "$1" ]; }; owned() { [ "$MODE" != not_owned ]; }; stage=manager_cleanup_sentinel; preflight_network_stage; result=$?; [ "$stage" = manager_cleanup_sentinel ] || { echo "probe overwrote transaction cleanup" >&2; exit 99; }; exit "$result"'
        env = dict(os.environ, BB=str(wrapper), USB=str(payload), MODE=mode)
        result = subprocess.run(['/bin/sh', '-c', command, 'fixture', str(script)], env=env, capture_output=True, timeout=10)
        assert result.returncode != 99, (mode, result.stderr)
        assert (result.returncode == 0) == (mode == 'valid'), (mode, result.stderr)
        passed('network_staging_' + mode)

# A standalone sshd from a removed or moved-aside installation keeps port 2222
# until the bar restarts. Only the sshd recorded by the SSH store (legacy
# migration source or the toolkit's own listener) may hold the port.
LISTEN_2222 = '   0: 00000000:08AE 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1 1 0000000000000000 100 0 0 10 0\n'
LISTEN_22 = '   1: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 2 1 0000000000000000 100 0 0 10 0\n'
CONNECTED_2222 = '   2: 0D01A8C0:08AE 0501A8C0:D431 01 00000000:00000000 00:00000000 00000000     0        0 3 1 0000000000000000 20 4 30 10 -1\n'
PEER_2222 = '   3: 0D01A8C0:D431 0501A8C0:08AE 01 00000000:00000000 00:00000000 00000000     0        0 4 1 0000000000000000 20 4 30 10 -1\n'
LISTEN6_2222 = '   0: 00000000000000000000000000000000:08AE 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 5 1 0000000000000000 100 0 0 10 0\n'
TCP_HEADER = '  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n'
port_cases = {
    'free': ([LISTEN_22], [], 'true', 'unowned', None),
    'listener_unowned': ([LISTEN_22, LISTEN_2222], [], 'true', 'unowned', 'SSH_PORT_2222_IN_USE_RESTART_BAR_FIRST'),
    'listener_unowned_ipv6': ([LISTEN_22], [LISTEN6_2222], 'true', 'unowned', 'SSH_PORT_2222_IN_USE_RESTART_BAR_FIRST'),
    'listener_owned_by_store': ([LISTEN_2222], [LISTEN6_2222], 'true', 'owned', None),
    'connected_session_only': ([CONNECTED_2222], [], 'true', 'unowned', None),
    'outgoing_peer_port_only': ([PEER_2222], [], 'true', 'unowned', None),
    'state_unreadable': (None, None, 'true', 'unowned', 'SSH_PORT_STATE_UNREADABLE'),
    'ssh_not_selected': ([LISTEN_2222], [], 'false', 'unowned', None),
}
for label, (tcp, tcp6, ssh, owner, reason) in port_cases.items():
    with tempfile.TemporaryDirectory(prefix='jbl-ssh-port-') as temp:
        folder = Path(temp); net = folder / 'net'; net.mkdir()
        if tcp is not None:
            (net / 'tcp').write_text(TCP_HEADER + ''.join(tcp))
            (net / 'tcp6').write_text(TCP_HEADER + ''.join(tcp6))
        (folder / 'files/ssh').mkdir(parents=True)
        (folder / 'files/ssh/common.sh').write_text('verify_tools() { return 0; }\n')
        script = folder / 'preflight.sh'
        script.write_text((ROOT / 'models/bar91/usb/preflight.sh').read_text().replace('/proc/net/', str(net) + '/'))
        command = ('. "$1"; regular() { [ -f "$1" ] && [ ! -L "$1" ]; }; '
                   'process_owned() { [ "$1" = "$SSH_STORE/sshd.pid" ] && [ "$3" = ssh ] && [ "$OWNER_CASE" = owned ]; }; '
                   'process_pid=manager_sentinel; preflight_ssh; result=$?; '
                   '[ "$process_pid" = manager_sentinel ] || { echo "probe overwrote process_pid" >&2; exit 99; }; '
                   'printf "%s\\n" "$PREFLIGHT_REASON"; exit "$result"')
        env = dict(os.environ, BB=BB, USB=str(folder), SSH=ssh, SSH_PASSWORD_HASH='fixture-hash',
                   SSH_STORE=str(folder / 'ssh-store'), OWNER_CASE=owner)
        result = subprocess.run(['/bin/sh', '-c', command, 'fixture', str(script)], env=env, capture_output=True, timeout=10)
        assert result.returncode != 99, (label, result.stderr)
        assert (result.returncode == 0) == (reason is None), (label, result.stdout, result.stderr)
        if reason is not None:
            assert result.stdout.decode().strip() == reason, (label, result.stdout)
        passed('ssh_port_preflight_' + label)

for script in (ROOT / 'models/bar91/usb').glob('*.sh'):
    subprocess.run([BB, 'sh', '-n', str(script)], check=True)
passed('all_device_shell_syntax')
out = ROOT / 'build/test-results'
out.mkdir(parents=True, exist_ok=True)
(out / 'usb-tests.json').write_text(json.dumps({'scope': 'Host fixtures only. Real ARM package build; native firmware, Wi-Fi and SSH are not executed on hardware.', 'cases': cases}, indent=2) + '\n')
