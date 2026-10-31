#!/usr/bin/env python3
import hashlib
import gzip
import importlib.util
import io
from pathlib import Path
import tarfile
import tempfile
import unittest

spec=importlib.util.spec_from_file_location('installer',Path(__file__).with_name('install-release.py'))
installer=importlib.util.module_from_spec(spec);spec.loader.exec_module(installer)

def archive(entries):
    output=io.BytesIO()
    with tarfile.open(fileobj=output,mode='w:gz') as tar:
        for name,data in entries:
            entry=tarfile.TarInfo(name);entry.size=len(data);entry.mode=0o755;tar.addfile(entry,io.BytesIO(data))
    return output.getvalue()

class InstallTests(unittest.TestCase):
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
