package managed

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

type RemoteOptions struct {
	StateDir, AgentID, Host, Directory, Executable string
	Layout                                         RemoteLayout
	Once                                           bool
	Resume                                         bool
	Stdout, Stderr                                 io.Writer
}

var sshHostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]{0,252}$`)

func shellWord(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

// RunRemote deploys into a new, explicitly selected directory and runs one
// fenced worker. SSH is the only network transport; the broker never listens
// on TCP. The remote host is trusted to protect this worker's data/token.
func RunRemote(ctx context.Context, o RemoteOptions) error {
	if !sshHostPattern.MatchString(o.Host) {
		return errors.New("invalid SSH host/alias")
	}
	root := filepath.Clean(o.Directory)
	if !filepath.IsAbs(root) || root == "/" || strings.ContainsRune(root, ':') || strings.IndexFunc(root, unicode.IsControl) >= 0 {
		return errors.New("remote directory must be a new absolute path without colons or control characters")
	}
	remoteState := filepath.Join(root, "state")
	remoteSocket := SocketPath(remoteState)
	if len(remoteSocket) > 100 {
		return errors.New("remote path exceeds portable Unix socket length")
	}
	s, err := Open(o.StateDir)
	if err != nil {
		return err
	}
	o.StateDir = s.dir
	if strings.ContainsRune(SocketPath(o.StateDir), ':') || strings.IndexFunc(SocketPath(o.StateDir), unicode.IsControl) >= 0 {
		s.Close()
		return errors.New("local forwarding socket path cannot contain colons or control characters")
	}
	if o.Layout.Workdir == "" {
		o.Layout.Workdir = filepath.Join(remoteState, "workspaces", o.AgentID)
	}
	definition, err := s.WorkerDefinition(o.AgentID, o.Layout)
	s.Close()
	if err != nil {
		return err
	}
	// Installation and its forwarding session share the already persisted
	// session deadline; a stalled deployment cannot outlive that allocation.
	remoteCtx, stopRemote := context.WithDeadline(ctx, definition.Deadline)
	defer stopRemote()
	ctx = remoteCtx
	credential, err := LoadCredential(CredentialPath(o.StateDir, o.AgentID))
	if err != nil {
		return err
	}
	credential.Socket = remoteSocket
	if credential.AgentID != o.AgentID {
		return errors.New("remote credential identity mismatch")
	}
	executable := o.Executable
	if executable == "" {
		executable, err = os.Executable()
		if err != nil {
			return err
		}
	}
	info, err := os.Lstat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 128<<20 {
		return errors.New("deployment binary must be a regular file no larger than 128 MiB")
	}
	binary, err := os.Open(executable)
	if err != nil {
		return err
	}
	defer binary.Close()
	var elfHeader [20]byte
	if _, err := io.ReadFull(binary, elfHeader[:]); err != nil || string(elfHeader[:4]) != "\x7fELF" || elfHeader[4] != 2 || elfHeader[5] != 1 || elfHeader[18] != 62 || elfHeader[19] != 0 {
		return errors.New("remote deployment requires a Linux amd64 ELF binary; select --binary for a cross-built artifact")
	}
	if _, err := binary.Seek(0, io.SeekStart); err != nil {
		return err
	}
	defBytes, err := json.Marshal(definition)
	if err != nil {
		return err
	}
	if len(defBytes) > 1<<20 {
		return errors.New("remote worker definition exceeds 1 MiB")
	}
	credBytes, _ := json.Marshal(credential)
	if len(credBytes) > 64<<10 {
		return errors.New("remote worker credential exceeds 64 KiB")
	}
	reader, writer := io.Pipe()
	mode := "new"
	if o.Resume {
		mode = "resume"
	}
	deploy := exec.CommandContext(ctx, "ssh", "-T", "-oBatchMode=yes", "-oConnectTimeout=10", "-oServerAliveInterval=5", "-oServerAliveCountMax=2", "--", o.Host, "python3 -c "+shellWord(remoteInstall)+" "+shellWord(root)+" "+shellWord(o.AgentID)+" "+shellWord(mode))
	deploy.Stdin = reader
	deploy.Stdout = o.Stderr
	deploy.Stderr = o.Stderr
	deploy.WaitDelay = 2e9
	if err := deploy.Start(); err != nil {
		reader.Close()
		writer.Close()
		return err
	}
	copyDone := make(chan error, 1)
	go func() {
		tw := tar.NewWriter(writer)
		put := func(name string, mode int64, data []byte) error {
			if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
				return err
			}
			_, err := tw.Write(data)
			return err
		}
		err := tw.WriteHeader(&tar.Header{Name: "agentmux", Mode: 0700, Size: info.Size(), Typeflag: tar.TypeReg})
		if err == nil {
			_, err = io.CopyN(tw, binary, info.Size())
		}
		if err == nil {
			err = put("worker.json", 0600, defBytes)
		}
		if err == nil {
			err = put("credential.json", 0600, credBytes)
		}
		if closeErr := tw.Close(); err == nil {
			err = closeErr
		}
		writer.CloseWithError(err)
		copyDone <- err
	}()
	deployErr := deploy.Wait()
	reader.Close()
	copyErr := <-copyDone
	if deployErr != nil {
		return errors.New("remote installation failed; inspect SSH stderr (existing directories are never replaced)")
	}
	if copyErr != nil {
		return copyErr
	}
	once := ""
	if o.Once {
		once = " --once"
	}
	command := "python3 -c " + shellWord(remoteSupervisor) + " " + shellWord(filepath.Join(root, "agentmux")) + " managed worker --state " + shellWord(remoteState) + " --agent " + shellWord(o.AgentID) + " --definition " + shellWord(filepath.Join(root, "worker.json")) + once
	// Keeping stdin open is the liveness channel. SSH disconnect/owner cancel
	// yields EOF remotely, where the supervisor terminates its process group.
	run := exec.CommandContext(ctx, "ssh", "-T", "-oBatchMode=yes", "-oConnectTimeout=10", "-oExitOnForwardFailure=yes", "-oServerAliveInterval=5", "-oServerAliveCountMax=2", "-R", remoteSocket+":"+SocketPath(o.StateDir), "--", o.Host, command)
	input, err := run.StdinPipe()
	if err != nil {
		return err
	}
	defer input.Close()
	run.Stdout = o.Stdout
	run.Stderr = o.Stderr
	run.WaitDelay = 7e9
	if err := run.Run(); err != nil {
		return fmt.Errorf("remote worker/SSH ended unsuccessfully; inspect status before any retry: %w", err)
	}
	return nil
}

const remoteInstall = `import os,sys,tarfile,platform,hashlib,stat,fcntl
root,agent,mode=sys.argv[1:]
if platform.system()!='Linux' or platform.machine() not in ('x86_64','AMD64'): raise SystemExit('deployment requires Linux amd64; cross-build support must be selected explicitly')
os.umask(0o077)
parent=os.path.dirname(root)
if not os.path.isdir(parent) or os.path.realpath(parent)!=parent: raise SystemExit('parent must already exist without symlinks')
# Ancestors owned by another user, or non-sticky shared writable ancestors,
# can replace a private child directory between installation and execution.
current=parent
root_owner=os.lstat('/').st_uid
while True:
 info=os.lstat(current)
 if info.st_uid not in (root_owner,os.getuid()) or info.st_mode&0o022 and not info.st_mode&stat.S_ISVTX: raise SystemExit('deployment ancestors must be owned by this user/root and not shared writable without sticky protection')
 if current=='/': break
 current=os.path.dirname(current)
resume=mode=='resume'
if resume:
 for path in (root,root+'/state',root+'/state/credentials',root+'/state/workspaces',root+'/state/workspaces/'+agent):
  info=os.lstat(path)
  if not stat.S_ISDIR(info.st_mode) or info.st_uid!=os.getuid() or info.st_mode&0o077 or os.path.realpath(path)!=path: raise SystemExit('resume requires existing owned private directories')
else:
 os.mkdir(root,0o700)
 os.mkdir(root+'/state',0o700)
 os.mkdir(root+'/state/credentials',0o700)
 os.mkdir(root+'/state/workspaces',0o700)
 os.mkdir(root+'/state/workspaces/'+agent,0o700)
lock=os.open(root+'/state/worker-'+agent+'.lock',os.O_RDWR|os.O_CREAT|os.O_NOFOLLOW|os.O_CLOEXEC,0o600)
lockinfo=os.fstat(lock)
if not stat.S_ISREG(lockinfo.st_mode) or lockinfo.st_uid!=os.getuid() or lockinfo.st_nlink!=1 or lockinfo.st_mode&0o077: raise SystemExit('unsafe worker lock')
try: fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
except BlockingIOError: raise SystemExit('remote worker is active; stop it before redeployment/resume')
seen=set()
class BoundedInput:
 def __init__(self,source): self.source=source;self.remaining=130*1024*1024
 def read(self,size=-1):
  if size<0 or size>self.remaining: raise ValueError('deployment wire bound exceeded')
  data=self.source.read(size);self.remaining-=len(data);return data
with tarfile.open(fileobj=BoundedInput(sys.stdin.buffer),mode='r|') as archive:
 for entry in archive:
  limits={'agentmux':134217728,'worker.json':1048576,'credential.json':65536}
  if entry.name not in limits or entry.name in seen or entry.type not in (tarfile.REGTYPE,tarfile.AREGTYPE) or entry.size<=0 or entry.size>limits[entry.name]: raise SystemExit('invalid deployment archive')
  seen.add(entry.name)
  path=root+('/state/credentials/'+agent+'.json' if entry.name=='credential.json' else '/'+entry.name)
  source=archive.extractfile(entry)
  if resume:
   info=os.lstat(path)
   if not stat.S_ISREG(info.st_mode) or info.st_uid!=os.getuid() or info.st_nlink!=1 or info.st_mode&0o077 or info.st_size!=entry.size: raise SystemExit('resume artifact changed or unsafe')
   wanted=hashlib.sha256();actual=hashlib.sha256()
   with open(path,'rb') as current:
    remaining=entry.size
    while remaining:
     block=source.read(min(remaining,1048576))
     if not block: raise SystemExit('truncated resume validation')
     wanted.update(block);actual.update(current.read(len(block)));remaining-=len(block)
   if wanted.digest()!=actual.digest(): raise SystemExit('resume requires identical binary, definition and credential; explicitly redeploy after changes')
   continue
  with open(path,'xb') as dest:
   remaining=entry.size
   while remaining:
    block=source.read(min(remaining,1048576))
    if not block: raise SystemExit('truncated deployment')
    dest.write(block);remaining-=len(block)
   dest.flush();os.fsync(dest.fileno())
  os.chmod(path,0o700 if entry.name=='agentmux' else 0o600)
if seen!={'agentmux','worker.json','credential.json'}: raise SystemExit('incomplete deployment')
socket=root+'/state/broker.sock'
if os.path.lexists(socket):
 info=os.lstat(socket)
 if not resume or not stat.S_ISSOCK(info.st_mode) or info.st_uid!=os.getuid(): raise SystemExit('unsafe existing forwarding path')
 os.unlink(socket)
print('Private worker deployment verified' if resume else 'Private worker deployment created',flush=True)
`

const remoteSupervisor = `import os,sys,subprocess,threading,signal,time
child=subprocess.Popen(sys.argv[1:],stdin=subprocess.DEVNULL,start_new_session=True)
def stop(*args):
 try: os.killpg(child.pid,signal.SIGTERM)
 except ProcessLookupError: return
 try: child.wait(timeout=5)
 except subprocess.TimeoutExpired:
  try: os.killpg(child.pid,signal.SIGKILL)
  except ProcessLookupError: pass
def monitor():
 try:
  while os.read(0,1): pass
 finally: stop()
threading.Thread(target=monitor,daemon=True).start()
signal.signal(signal.SIGTERM,stop);signal.signal(signal.SIGHUP,stop)
raise SystemExit(child.wait())
`
