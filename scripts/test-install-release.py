#!/usr/bin/env python3
import hashlib
import gzip
import importlib.util
import io
import os
from pathlib import Path
import platform
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest import mock

spec=importlib.util.spec_from_file_location('installer',Path(__file__).with_name('install-release.py'))
installer=importlib.util.module_from_spec(spec);spec.loader.exec_module(installer)

def archive(entries):
    output=io.BytesIO()
    with tarfile.open(fileobj=output,mode='w:gz') as tar:
        for name,data in entries:
            entry=tarfile.TarInfo(name);entry.size=len(data);entry.mode=0o755;tar.addfile(entry,io.BytesIO(data))
    return output.getvalue()

class InstallTests(unittest.TestCase):
    binary=b'#!/bin/sh\nprintf "agentmux fixture\\n"\n'

    def test_install_creates_relative_executable_shortcut(self):
        with tempfile.TemporaryDirectory() as root:
            root=Path(root);destination=root/'agentmux'
            self.assertEqual(installer.install_binary(self.binary,destination),'agentmux fixture')
            alias=root/'amx'
            self.assertTrue(alias.is_symlink())
            self.assertEqual(os.readlink(alias),'agentmux')
            self.assertEqual(subprocess.check_output([alias,'version'],text=True),'agentmux fixture\n')
            self.assertEqual(destination.stat().st_mode&0o777,0o755)

    def test_unrelated_shortcuts_are_preserved_even_with_replace(self):
        for kind in ('file','directory','symlink','dangling-symlink'):
            with self.subTest(kind=kind),tempfile.TemporaryDirectory() as root:
                root=Path(root);destination=root/'agentmux';destination.write_bytes(b'original')
                alias=root/'amx'
                if kind=='file':alias.write_bytes(b'other command')
                elif kind=='directory':alias.mkdir()
                else:alias.symlink_to('other-command' if kind=='symlink' else 'missing')
                with mock.patch.object(installer,'probe_version') as probe:
                    with self.assertRaisesRegex(ValueError,'amx already exists'):
                        installer.install_binary(self.binary,destination,True)
                    probe.assert_not_called()
                self.assertEqual(destination.read_bytes(),b'original')
                self.assertTrue(os.path.lexists(alias))
                self.assertFalse(list(root.glob('agentmux.backup-*')))
                self.assertFalse(list(root.glob('.agentmux-install-*')))

    def test_upgrade_preserves_old_inode_and_reuses_existing_alias(self):
        for link in ('agentmux','absolute'):
            with self.subTest(link=link),tempfile.TemporaryDirectory() as root:
                root=Path(root);destination=root/'agentmux';destination.write_bytes(b'old executable');destination.chmod(0o751)
                old=destination.stat();alias=root/'amx';alias.symlink_to(destination if link=='absolute' else link)
                alias_inode=alias.lstat().st_ino
                installer.install_binary(self.binary,destination,True)
                backup,=root.glob('agentmux.backup-*')
                self.assertEqual(backup.read_bytes(),b'old executable')
                self.assertEqual(backup.stat().st_ino,old.st_ino)
                self.assertEqual(backup.stat().st_mode&0o777,0o751)
                self.assertEqual(alias.lstat().st_ino,alias_inode)
                self.assertEqual(alias.read_bytes(),self.binary)

    def test_lexically_equivalent_but_indirect_shortcut_is_refused(self):
        for link in ('missing/../agentmux','./agentmux','other/../agentmux'):
            with self.subTest(link=link),tempfile.TemporaryDirectory() as root:
                root=Path(root);destination=root/'agentmux';destination.write_bytes(b'original');alias=root/'amx';alias.symlink_to(link)
                with self.assertRaisesRegex(ValueError,'does not point directly'):
                    installer.install_binary(self.binary,destination,True)
                self.assertEqual(destination.read_bytes(),b'original')
                self.assertEqual(os.readlink(alias),link)

    def test_alias_collision_created_during_probe_preserves_both_commands(self):
        for original_alias in (False,True):
            with self.subTest(original_alias=original_alias),tempfile.TemporaryDirectory() as root:
                root=Path(root);destination=root/'agentmux';destination.write_bytes(b'original');alias=root/'amx'
                if original_alias:alias.symlink_to('agentmux')
                def collision(_):
                    if os.path.lexists(alias):alias.unlink()
                    alias.write_bytes(b'concurrent command')
                    return 'agentmux fixture\n'
                with mock.patch.object(installer,'probe_version',side_effect=collision):
                    with self.assertRaisesRegex(ValueError,'amx already exists'):
                        installer.install_binary(self.binary,destination,True)
                self.assertEqual(destination.read_bytes(),b'original')
                self.assertEqual(alias.read_bytes(),b'concurrent command')
                self.assertFalse(list(root.glob('agentmux.backup-*')))

    def test_backup_must_retain_the_original_inode_before_commit(self):
        for swapped in ('before-link','after-link'):
            with self.subTest(swapped=swapped),tempfile.TemporaryDirectory() as root:
                root=Path(root);destination=root/'agentmux';destination.write_bytes(b'original')
                real_link=os.link
                def racing_link(source,dest,**kwargs):
                    backup=Path(dest).name.startswith('agentmux.backup-')
                    def change():
                        other=root/'other';other.write_bytes(b'concurrent command');os.replace(other,destination)
                    if backup and swapped=='before-link':change()
                    result=real_link(source,dest,**kwargs)
                    if backup and swapped=='after-link':change()
                    return result
                with mock.patch.object(installer.os,'link',side_effect=racing_link):
                    with self.assertRaisesRegex(ValueError,'while retaining its backup'):
                        installer.install_binary(self.binary,destination,True)
                self.assertEqual(destination.read_bytes(),b'concurrent command')
                backup,=root.glob('agentmux.backup-*')
                self.assertEqual(backup.read_bytes(),b'concurrent command' if swapped=='before-link' else b'original')
                self.assertFalse(os.path.lexists(root/'amx'))

    def test_existing_dangling_agentmux_shortcut_is_repaired_by_install(self):
        with tempfile.TemporaryDirectory() as root:
            root=Path(root);destination=root/'agentmux';alias=root/'amx';alias.symlink_to('agentmux')
            inode=alias.lstat().st_ino
            installer.install_binary(self.binary,destination)
            self.assertEqual(alias.lstat().st_ino,inode)
            self.assertEqual(alias.read_bytes(),self.binary)

    def test_concurrent_destination_change_is_not_overwritten(self):
        for existing in (False,True):
            with self.subTest(existing=existing),tempfile.TemporaryDirectory() as root:
                root=Path(root);destination=root/'agentmux'
                if existing:destination.write_bytes(b'original')
                def changed(_):
                    other=root/'new-command';other.write_bytes(b'concurrent command');os.replace(other,destination)
                    return 'agentmux fixture\n'
                with mock.patch.object(installer,'probe_version',side_effect=changed):
                    with self.assertRaises((ValueError,FileExistsError)):
                        installer.install_binary(self.binary,destination,existing)
                self.assertEqual(destination.read_bytes(),b'concurrent command')
                self.assertFalse(os.path.lexists(root/'amx'))
                self.assertFalse(list(root.glob('agentmux.backup-*')))

    def test_failure_rolls_back_new_alias_without_losing_existing_commands(self):
        for existing in (False,True):
            with self.subTest(existing=existing),tempfile.TemporaryDirectory() as root:
                root=Path(root);destination=root/'agentmux'
                if existing:destination.write_bytes(b'original')
                mutation='replace' if existing else 'link'
                with mock.patch.object(installer.os,mutation,side_effect=OSError('fixture commit failure')):
                    with self.assertRaisesRegex(OSError,'fixture commit failure'):
                        installer.install_binary(self.binary,destination,existing)
                self.assertFalse(os.path.lexists(root/'amx'))
                if existing:self.assertEqual(destination.read_bytes(),b'original')
                else:self.assertFalse(destination.exists())
                self.assertFalse(list(root.glob('.agentmux-install-*')))

    def test_direct_amx_destination_does_not_self_link(self):
        with tempfile.TemporaryDirectory() as root:
            destination=Path(root)/'amx'
            installer.install_binary(self.binary,destination)
            self.assertFalse(destination.is_symlink())
            self.assertEqual(subprocess.check_output([destination,'version'],text=True),'agentmux fixture\n')

    def test_local_source_installer_shares_safe_alias_and_upgrade_behavior(self):
        with tempfile.TemporaryDirectory() as root:
            root=Path(root);source=root/'source';source.write_bytes(self.binary);destination=root/'agentmux'
            command=[sys.executable,str(Path(__file__).with_name('install-local.py')),'--source',str(source),'--destination',str(destination)]
            first=subprocess.run(command,capture_output=True,text=True,timeout=10)
            self.assertEqual(first.returncode,0,first.stderr)
            self.assertEqual((root/'amx').read_bytes(),self.binary)
            second=subprocess.run(command+['--replace'],capture_output=True,text=True,timeout=10)
            self.assertEqual(second.returncode,0,second.stderr)
            self.assertEqual(len(list(root.glob('agentmux.backup-*'))),1)

    def test_cli_refused_overwrite_is_concise_and_preserves_destination(self):
        system={'Linux':'linux','Darwin':'darwin'}.get(platform.system())
        arch={'x86_64':'x86_64','AMD64':'x86_64','aarch64':'arm64','arm64':'arm64'}.get(platform.machine())
        if not system or not arch:self.skipTest('release installer platform unsupported')
        name='agentmux_'+system+'_'+arch+'.tar.gz'
        payload=archive([('agentmux',b'#!/bin/sh\nprintf "agentmux 1.2.3\\n"\n')])
        with tempfile.TemporaryDirectory() as root:
            root=Path(root);destination=root/'agentmux';original=b'existing executable'
            destination.write_bytes(original)
            (root/name).write_bytes(payload)
            (root/'checksums.txt').write_text(hashlib.sha256(payload).hexdigest()+'  '+name+'\n')
            result=subprocess.run([sys.executable,str(Path(__file__).with_name('install-release.py')),'--version','v1.2.3','--destination',str(destination),'--artifact-dir',str(root)],capture_output=True,text=True,timeout=10)
            self.assertEqual(result.returncode,1)
            self.assertEqual(result.stdout,'')
            self.assertEqual(result.stderr,'error: destination exists; use --replace explicitly\n')
            self.assertEqual(destination.read_bytes(),original)

    def test_cli_preserves_unexpected_programming_errors(self):
        with mock.patch.object(installer,'main',side_effect=RuntimeError('unexpected bug')):
            with self.assertRaisesRegex(RuntimeError,'unexpected bug'):installer.cli()

    def test_expected_version_removes_only_one_optional_prefix(self):
        payload=archive([('agentmux',b'#!/bin/sh\nprintf "agentmux 1.2.3\\n"\n')]);name='a.tar.gz'
        manifest=(hashlib.sha256(payload).hexdigest()+'  '+name).encode()
        with tempfile.TemporaryDirectory() as root:
            destination=Path(root)/'agentmux'
            installer.install(payload,manifest,name,destination,False,'v1.2.3')
            installer.install(payload,manifest,name,destination,True,'1.2.3')
            with self.assertRaisesRegex(ValueError,'does not match'):installer.install(payload,manifest,name,destination,True,'vv1.2.3')

    def test_version_probe_output_and_inherited_pipe_lifetime_bounded(self):
        with tempfile.TemporaryDirectory() as root:
            path=Path(root)/'probe';path.write_text('#!/bin/sh\nprintf "%70000s" x\n');path.chmod(0o700)
            with self.assertRaisesRegex(ValueError,'output'):installer.probe_version(path)
            path.write_text('#!/bin/sh\nsleep 30 &\nexit 0\n')
            old=installer.PROBE_TIMEOUT_SECONDS
            try:
                installer.PROBE_TIMEOUT_SECONDS=0.1
                with self.assertRaisesRegex(ValueError,'timed out'):installer.probe_version(path)
            finally:installer.PROBE_TIMEOUT_SECONDS=old

    def test_hidden_metadata_and_decompression_are_bounded_before_parser(self):
        header=tarfile.TarInfo('pax');header.type=tarfile.XHDTYPE;header.size=65537
        payload=gzip.compress(header.tobuf()+b'x'*65537+bytes(2048));name='metadata.tar.gz';manifest=(hashlib.sha256(payload).hexdigest()+'  '+name).encode()
        with tempfile.TemporaryDirectory() as root:
            with self.assertRaisesRegex(ValueError,'metadata'):installer.install(payload,manifest,name,Path(root)/'agentmux',False)
        old=installer.MAX_EXPANDED_BYTES
        try:
            installer.MAX_EXPANDED_BYTES=1024
            with self.assertRaisesRegex(ValueError,'expanded'):installer.bounded_tar(gzip.compress(bytes(1025)))
        finally:installer.MAX_EXPANDED_BYTES=old

    def test_atomic_explicit_replacement_and_checksum(self):
        payload=archive([('agentmux',b'#!/bin/sh\nprintf "agentmux fixture\\n"\n')]);name='agentmux_linux_x86_64.tar.gz';manifest=(hashlib.sha256(payload).hexdigest()+'  '+name+'\n').encode()
        with tempfile.TemporaryDirectory() as root:
            destination=Path(root)/'agentmux'
            installer.install(payload,manifest,name,destination,False)
            original=destination.read_bytes()
            with self.assertRaises(ValueError):installer.install(payload,manifest,name,destination,False)
            with self.assertRaises(ValueError):installer.install(payload+b'x',manifest,name,destination,True)
            with self.assertRaises(ValueError):installer.install(payload,manifest,name,destination,True,'v0.10.0-rc.1')
            self.assertEqual(original,destination.read_bytes())
            installer.install(payload,manifest,name,destination,True)
            link=Path(root)/'link';link.symlink_to(destination)
            with self.assertRaises(ValueError):installer.install(payload,manifest,name,link,True)

    def test_duplicate_and_traversal_do_not_install(self):
        for entries in [[('agentmux',b'x'),('agentmux',b'y')],[('../agentmux',b'x')]]:
            payload=archive(entries);name='a.tar.gz';manifest=(hashlib.sha256(payload).hexdigest()+'  '+name).encode()
            with tempfile.TemporaryDirectory() as root:
                target=Path(root)/'agentmux'
                with self.assertRaises(ValueError):installer.install(payload,manifest,name,target,False)
                self.assertFalse(target.exists())

if __name__=='__main__':unittest.main()
