#!/usr/bin/env python3
"""Install an explicit Agentmux release after checking its published SHA256.

Downloads artifacts only; never executes downloaded installer scripts.
amx is the canonical executable. Legacy archives remain supported. Existing
binaries require --replace and are retained as backups; unrelated shortcuts
are never replaced.
"""
import argparse
import hashlib
import gzip
import io
import os
from pathlib import Path
import platform
import re
import selectors
import secrets
import signal
import stat
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request

MAX_EXPANDED_BYTES=256*1024*1024
PROBE_TIMEOUT_SECONDS=10

def probe_version(path):
    process=subprocess.Popen([path,'version'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
    selector=selectors.DefaultSelector();selector.register(process.stdout,selectors.EVENT_READ);selector.register(process.stderr,selectors.EVENT_READ)
    deadline=time.monotonic()+PROBE_TIMEOUT_SECONDS;size=0;output=[]
    try:
        while selector.get_map():
            remaining=deadline-time.monotonic()
            if remaining<=0:raise ValueError('executable version probe timed out')
            for key,_ in selector.select(remaining):
                block=os.read(key.fileobj.fileno(),8192)
                if not block:selector.unregister(key.fileobj);continue
                size+=len(block)
                if size>65536:raise ValueError('executable version output exceeds bound')
                if key.fileobj is process.stdout:output.append(block)
        try:code=process.wait(timeout=max(0.001,deadline-time.monotonic()))
        except subprocess.TimeoutExpired:raise ValueError('executable version probe timed out') from None
        if code!=0:raise ValueError('executable version probe failed')
        return b''.join(output).decode('utf-8')
    finally:
        # A child which exited after starting a helper may leave captured pipes
        # open. Kill its whole process group on timeout/overflow, not just the
        # original process, and never wait indefinitely on inherited pipes.
        if process.returncode is None:
            try:os.killpg(process.pid,signal.SIGKILL)
            except ProcessLookupError:pass
        try:process.wait(timeout=2)
        except subprocess.TimeoutExpired:pass
        process.stdout.close();process.stderr.close();selector.close()

def bounded_tar(archive):
    # Bound decompression before tarfile can parse PAX/GNU metadata: those
    # records are consumed internally and never reach our yielded-entry loop.
    with gzip.GzipFile(fileobj=io.BytesIO(archive)) as stream:
        expanded=stream.read(MAX_EXPANDED_BYTES+1)
    if len(expanded)>MAX_EXPANDED_BYTES:raise ValueError('expanded archive exceeds bound')
    offset=0;headers=0;metadata=0
    while offset+512<=len(expanded):
        block=expanded[offset:offset+512]
        if block==bytes(512):break
        header=tarfile.TarInfo.frombuf(block,'utf-8','surrogateescape')
        headers+=1
        if headers>1000 or header.size<0 or header.size>MAX_EXPANDED_BYTES:raise ValueError('archive header bound exceeded')
        if header.type in (tarfile.XHDTYPE,tarfile.XGLTYPE,tarfile.GNUTYPE_LONGNAME,tarfile.GNUTYPE_LONGLINK):
            metadata+=header.size
            if header.size>65536 or metadata>1048576:raise ValueError('archive metadata exceeds bound')
        offset+=512+((header.size+511)//512)*512
        if offset>len(expanded):raise ValueError('truncated archive entry')
    return expanded

def install(archive,manifest,name,destination,replace,expected_version=None):
    if len(archive)>128*1024*1024 or len(manifest)>1024*1024: raise ValueError('release payload exceeds bound')
    matches=[]
    for line in manifest.decode('ascii').splitlines():
        parts=line.split()
        if len(parts)==2 and parts[1].lstrip('*')==name:matches.append(parts[0])
    if len(matches)!=1 or not re.fullmatch('[0-9a-fA-F]{64}',matches[0]) or hashlib.sha256(archive).hexdigest()!=matches[0].lower():raise ValueError('release checksum missing, ambiguous, or mismatched')
    with tarfile.open(fileobj=io.BytesIO(bounded_tar(archive)),mode='r:') as tar:
        entries=[];expanded=0
        for i,entry in enumerate(tar):
            if i>=1000:raise ValueError('archive has too many entries')
            expanded+=entry.size
            if entry.size<0 or expanded>256*1024*1024:raise ValueError('expanded archive exceeds bound')
            if entry.name in ('amx','agentmux'):entries.append(entry)
        canonical=[entry for entry in entries if entry.name=='amx']
        if canonical:entries=canonical
        if len(entries)!=1 or not entries[0].isfile() or not 0<entries[0].size<=128*1024*1024:raise ValueError('archive must contain one regular amx executable (legacy agentmux archives are supported)')
        stream=tar.extractfile(entries[0]);binary=stream.read(128*1024*1024+1)
        if len(binary)!=entries[0].size:raise ValueError('truncated executable')
    return install_binary(binary,destination,replace,expected_version)

def validate_alias(alias,target):
    if not alias.is_symlink() or os.readlink(alias) not in (target.name,str(target)):
        raise ValueError('amx already exists and does not point directly to this destination; move it explicitly or choose another directory')

def install_binary(binary,destination,replace=False,expected_version=None):
    """Install a verified executable and relative alias without truncating files.

    Both installation paths use this transaction. A sibling amx collision is
    checked before any executable is run or destination changed. A fresh alias
    is created without overwrite before the binary commit, then removed on a
    failed commit. Upgrades preserve the old inode at a unique backup path.
    """
    if not 0<len(binary)<=128*1024*1024:raise ValueError('executable exceeds bound')
    target=Path(destination).absolute();parent=target.parent.resolve(strict=True);target=parent/target.name
    original=None;legacy=None;legacy_original=None
    try:original=target.lstat()
    except FileNotFoundError:pass
    if original is not None:
        if not replace:raise ValueError('destination exists; use --replace explicitly')
        if stat.S_ISLNK(original.st_mode):
            if target.name!='amx' or os.readlink(target) not in ('agentmux',str(parent/'agentmux')):raise ValueError('refusing a symlink destination')
            legacy=parent/'agentmux';legacy_original=legacy.lstat()
            if not stat.S_ISREG(legacy_original.st_mode):raise ValueError('legacy agentmux destination must be a regular executable')
            if not re.match(r'^(?:agentmux|amx)\s',probe_version(str(legacy))):raise ValueError('legacy shortcut does not identify an Agentmux executable')
        elif not stat.S_ISREG(original.st_mode):raise ValueError('destination must be a regular executable')
    alias=parent/'amx' if target.name!='amx' else None
    if alias is not None and os.path.lexists(alias):validate_alias(alias,target)
    fd,path=tempfile.mkstemp(prefix='.agentmux-install-',dir=parent)
    alias_identity=None;backup=None;committed=False
    try:
        with os.fdopen(fd,'wb') as out:out.write(binary);out.flush();os.fsync(out.fileno())
        os.chmod(path,0o755)
        version=probe_version(path)
        if not re.match(r'^(?:agentmux|amx)\s',version):raise ValueError('unexpected executable version response')
        if expected_version:
            release_version=expected_version[1:] if expected_version.startswith('v') else expected_version
            if not re.match(r'^(?:amx|agentmux)\s+v?'+re.escape(release_version)+r'(?:\s|$)',version):raise ValueError('executable version does not match selected release')
        # No overwrite: another installation or unrelated command wins a race.
        if alias is not None and not os.path.lexists(alias):
            # Publish a known symlink inode, so rollback cannot mistake a
            # concurrently replaced command for the alias we created.
            staged_alias=path+'-amx'
            try:
                os.symlink(target.name,staged_alias)
                staged_identity=os.lstat(staged_alias)
                os.link(staged_alias,alias,follow_symlinks=False)
                alias_identity=staged_identity
            finally:
                if os.path.lexists(staged_alias):os.unlink(staged_alias)
        if alias is not None:validate_alias(alias,target)
        if original is not None:
            current=target.lstat()
            if (current.st_dev,current.st_ino)!=(original.st_dev,original.st_ino):
                raise ValueError('destination changed during installation; retry after inspecting it')
            backup=parent/(target.name+'.backup-'+secrets.token_hex(6))
            os.link(legacy or target,backup,follow_symlinks=False)
            saved=backup.lstat();current=target.lstat()
            expected=legacy_original or original
            if (saved.st_dev,saved.st_ino)!=(expected.st_dev,expected.st_ino) or (current.st_dev,current.st_ino)!=(original.st_dev,original.st_ino):
                raise ValueError('destination changed while retaining its backup; no executable was replaced')
            if legacy is not None:
                check=legacy.lstat()
                if (check.st_dev,check.st_ino)!=(expected.st_dev,expected.st_ino):raise ValueError('legacy executable changed during migration; no executable was replaced')
            if alias is not None:validate_alias(alias,target)
            os.replace(path,target)
        else:
            os.link(path,target);os.unlink(path)
        committed=True
        if legacy is not None:
            # Retire only the exact executable which backed our old shortcut.
            # Its original inode remains at the unique recovery backup.
            try:
                current=legacy.lstat()
                if (current.st_dev,current.st_ino)==(legacy_original.st_dev,legacy_original.st_ino):legacy.unlink()
            except OSError:
                print('amx installed; legacy agentmux path could not be retired; inspect it separately',file=sys.stderr)
        return version.strip()
    finally:
        if os.path.exists(path):os.unlink(path)
        if not committed:
            if alias_identity is not None:
                try:
                    current=alias.lstat()
                    if (current.st_dev,current.st_ino)==(alias_identity.st_dev,alias_identity.st_ino):alias.unlink()
                except FileNotFoundError:pass
            # Only remove our redundant backup if the old destination remains.
            # A concurrently changed destination must retain that recovery copy.
            if backup is not None:
                try:
                    current=target.lstat();saved=backup.lstat()
                    expected=legacy_original or original
                    if (current.st_dev,current.st_ino)==(original.st_dev,original.st_ino) and (saved.st_dev,saved.st_ino)==(expected.st_dev,expected.st_ino):backup.unlink()
                except FileNotFoundError:pass

def download(url,limit):
    request=urllib.request.Request(url,headers={'User-Agent':'agentmux-release-installer'})
    with urllib.request.urlopen(request,timeout=60) as response:
        # GitHub redirects artifact downloads to its release asset service.
        if response.url.split(':',1)[0]!='https':raise ValueError('HTTPS required')
        data=response.read(limit+1)
        if len(data)>limit:raise ValueError('download exceeds limit')
        return data

def read_bounded(path,limit):
    with open(path,'rb') as f:
        data=f.read(limit+1)
    if len(data)>limit:raise ValueError('release file exceeds limit')
    return data

def main():
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--version',required=True);p.add_argument('--destination',required=True);p.add_argument('--replace',action='store_true');p.add_argument('--artifact-dir',help='verify existing local release files without downloading');a=p.parse_args()
    if not re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?',a.version):p.error('explicit semver tag required')
    system={'Linux':'linux','Darwin':'darwin'}.get(platform.system());arch={'x86_64':'x86_64','AMD64':'x86_64','aarch64':'arm64','arm64':'arm64'}.get(platform.machine())
    if not system or not arch:p.error('supported releases: Linux/macOS amd64/arm64')
    name=f'agentmux_{system}_{arch}.tar.gz'
    if a.artifact_dir:
        root=Path(a.artifact_dir);archive=read_bounded(root/name,128*1024*1024);manifest=read_bounded(root/'checksums.txt',1024*1024)
    else:
        base=f'https://github.com/pagel-s/agentmux/releases/download/{a.version}/';manifest=download(base+'checksums.txt',1024*1024);archive=download(base+name,128*1024*1024)
    print(install(archive,manifest,name,a.destination,a.replace,a.version))

def cli():
    try:
        main()
    except (ValueError,OSError,tarfile.TarError,EOFError) as error:
        print('error: '+str(error),file=sys.stderr)
        return 1
    return 0

if __name__=='__main__':sys.exit(cli())
