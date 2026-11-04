"""Linux process handles for the workflow evidence runners.

Only pidfds admitted from a still-pinned parent may become signal targets.
The observation boundary is the trees actually seen through /proc; this is not
a containment mechanism for arbitrary hostile executables or an OS task quota.
"""
import errno
import os
from pathlib import Path
import select
import signal
import subprocess
import sys


MAX_PROCESSES = 512


def require_process_support():
    if (sys.platform != "linux" or not hasattr(os, "pidfd_open")
            or not hasattr(signal, "pidfd_send_signal")
            or not Path("/proc/self/task").is_dir()):
        raise RuntimeError("workflow evidence runners require Linux /proc and Python pidfd signaling; no unsafe fallback is used")
    try:
        descriptor = os.pidfd_open(os.getpid())
    except OSError as error:
        raise RuntimeError("Linux pidfd admission is unavailable; no unsafe fallback is used") from error
    try:
        # Signal zero checks the signaling syscall and current permission
        # without delivering a signal. This is admission preflight, not a
        # guarantee that every later target remains signalable.
        signal.pidfd_send_signal(descriptor, 0)
    except OSError as error:
        raise RuntimeError("Linux pidfd signaling is unavailable; no unsafe fallback is used") from error
    finally:
        os.close(descriptor)


def launch_pinned(command, **options):
    """Keep the executable behind an admission gate until its pidfd is held.

    A failed pin closes the gate: the child exits without executing the target,
    so cleanup never needs an unsafe numeric signal as an admission fallback.
    The wrapper execs the target in place and does not add a running controller.
    """
    gate_read, gate_write = os.pipe()
    process, tree = None, None
    gate_code = ("import os,sys; fd=int(sys.argv[1]); admitted=os.read(fd,1); os.close(fd); "
                 "sys.exit(125) if admitted!=b'1' else os.execvpe(sys.argv[2],sys.argv[2:],os.environ)")
    try:
        inherited = tuple(options.pop("pass_fds", ()))
        process = subprocess.Popen([sys.executable, "-I", "-B", "-c", gate_code, str(gate_read), *map(str, command)],
                                   pass_fds=(*inherited, gate_read), **options)
        os.close(gate_read)
        gate_read = None
        tree = process._validation_tree = ProcessTree(process)
        os.write(gate_write, b"1")
        os.close(gate_write)
        gate_write = None
        return process
    except BaseException:
        if gate_write is not None:
            os.close(gate_write)
            gate_write = None
        if process is not None:
            cleanup_errors = []
            try:
                if tree is not None:
                    try:
                        tree.observe()
                    except BaseException as error:
                        cleanup_errors.append(type(error).__name__)
                    try:
                        tree.signal(signal.SIGKILL)
                    except BaseException as error:
                        cleanup_errors.append(type(error).__name__)
                try:
                    process.communicate(timeout=5)
                except BaseException as error:
                    cleanup_errors.append(type(error).__name__)
            finally:
                if tree is not None:
                    tree.close()
            if cleanup_errors:
                raise RuntimeError("process admission cleanup failed: " + "; ".join(cleanup_errors))
        raise
    finally:
        for descriptor in (gate_read, gate_write):
            if descriptor is not None:
                os.close(descriptor)


def _read_process_identity(pid):
    data = Path("/proc", str(pid), "stat").read_text()
    tail = data[data.rindex(")") + 2:].split()
    return {"pid": pid, "start_ticks": tail[19], "state": tail[0], "parent_pid": int(tail[1])}


def process_identity(pid):
    try:
        return _read_process_identity(pid)
    except (OSError, ValueError, IndexError):
        return None


class PinnedProcess:
    def __init__(self, identity, descriptor):
        self.identity, self.descriptor = identity, descriptor

    @classmethod
    def open(cls, pid, *, parent_pid=None):
        before = process_identity(pid)
        if before is None or (parent_pid is not None and before["parent_pid"] != parent_pid):
            return None
        try:
            descriptor = os.pidfd_open(pid)
        except ProcessLookupError:
            return None
        except OSError as error:
            # Some kernels return EINVAL, rather than ESRCH, if the pid still
            # exists during lookup but loses its task before pidfd_prepare().
            # Discard only a *proven* vanished/recycled identity. A denied or
            # malformed /proc read and an unchanged live target remain fatal;
            # they must never turn unsupported admission into passing evidence.
            if error.errno == errno.EINVAL:
                try:
                    after = _read_process_identity(pid)
                except (FileNotFoundError, ProcessLookupError):
                    return None
                except (OSError, ValueError, IndexError):
                    pass
                else:
                    if after["start_ticks"] != before["start_ticks"]:
                        return None
            raise RuntimeError("pidfd_open failed for observed process "
                               f"pid={pid} start_ticks={before['start_ticks']} "
                               f"parent_pid={before['parent_pid']}: {error}") from error
        handle = cls(before, descriptor)
        after = handle.current()
        if (after is None or after["start_ticks"] != before["start_ticks"]
                or (parent_pid is not None and after["parent_pid"] != parent_pid)):
            handle.close()
            return None
        return handle

    def current(self):
        if self.descriptor is None:
            return None
        # fdinfo binds the /proc identity read to this pidfd even when a numeric
        # PID was recycled between opening the handle and reading /proc/stat.
        try:
            fields = dict(line.split(":", 1) for line in Path("/proc/self/fdinfo", str(self.descriptor)).read_text().splitlines() if ":" in line)
            if int(fields["Pid"].strip()) != self.identity["pid"]:
                return None
            current = process_identity(self.identity["pid"])
            fields = dict(line.split(":", 1) for line in Path("/proc/self/fdinfo", str(self.descriptor)).read_text().splitlines() if ":" in line)
            if int(fields["Pid"].strip()) != self.identity["pid"]:
                return None
        except (OSError, KeyError, ValueError):
            return None
        return current if current is not None and current["start_ticks"] == self.identity["start_ticks"] else None

    def live(self):
        current = self.current()
        return current is not None and current["state"] != "Z"

    def exited(self):
        """Confirm exit through the admitted pidfd, never an ambiguous /proc read.

        Linux reports POLLIN after the last thread exits and POLLHUP once the
        task is reaped. Both refer to this kernel identity even if its numeric
        PID has since been reused. Unknown or failed polling stays fatal.
        """
        if self.descriptor is None:
            raise RuntimeError("cannot poll a closed admitted pidfd")
        poller = select.poll()
        poller.register(self.descriptor, select.POLLIN)
        events = poller.poll(0)
        if not events:
            return False
        if (len(events) != 1 or events[0][0] != self.descriptor
                or not events[0][1] or events[0][1] & ~(select.POLLIN | select.POLLHUP)):
            raise RuntimeError("unexpected admitted pidfd polling result")
        return True

    def send_signal(self, number):
        if self.descriptor is None:
            return False
        try:
            signal.pidfd_send_signal(self.descriptor, number)
        except ProcessLookupError:
            return False
        return True

    def close(self):
        if self.descriptor is not None:
            os.close(self.descriptor)
            self.descriptor = None


class ProcessTree:
    def __init__(self, process):
        # Popen's unreaped direct child pins its PID during this admission.
        # Never reconstruct a handle from a reaped Popen's stale number.
        if process.returncode is not None:
            raise RuntimeError("cannot admit a reaped subprocess identity")
        self.root = PinnedProcess.open(process.pid, parent_pid=os.getpid())
        if self.root is None:
            raise RuntimeError("cannot pin the admitted subprocess identity")
        self.handles = {(process.pid, self.root.identity["start_ticks"]): self.root}

    def retire_exited(self):
        # Descendants have their own admitted pidfds: retiring an exited parent
        # must neither discard them nor authorize adoption through its old PID.
        for key, handle in list(self.handles.items()):
            if handle.exited():
                handle.close()
                del self.handles[key]

    def observe(self):
        self.retire_exited()
        pending, visited = list(self.handles.values()), set()
        while pending:
            parent = pending.pop()
            key = (parent.identity["pid"], parent.identity["start_ticks"])
            if key in visited:
                continue
            visited.add(key)
            if len(visited) > MAX_PROCESSES:
                raise RuntimeError(f"fixture process observation exceeded {MAX_PROCESSES} processes")
            if parent.current() is None:
                continue
            captured = []
            try:
                tasks = list(Path("/proc", str(parent.identity["pid"]), "task").iterdir())
                if len(tasks) > 4096:
                    raise RuntimeError("fixture process exceeded 4096 task threads")
                children = set()
                for task in tasks:
                    try:
                        children.update(int(child) for child in (task / "children").read_text().split())
                    except (OSError, ValueError):
                        continue
                if len(children) > MAX_PROCESSES:
                    raise RuntimeError(f"fixture process tree exceeded {MAX_PROCESSES} processes")
                for pid in children:
                    handle = PinnedProcess.open(pid, parent_pid=parent.identity["pid"])
                    if handle is not None:
                        captured.append(handle)
                # A reused root must never authorize traversal/adoption of its
                # new owner's children, even if it changed during this scan.
                if parent.current() is None:
                    continue
                for handle in captured:
                    identity = handle.identity
                    child_key = (identity["pid"], identity["start_ticks"])
                    if child_key not in self.handles:
                        self.handles[child_key] = handle
                    else:
                        handle.close()
                    pending.append(self.handles[child_key])
                captured = []
                if len(self.handles) > MAX_PROCESSES:
                    self.retire_exited()
                    if len(self.handles) > MAX_PROCESSES:
                        raise RuntimeError(f"fixture process tree exceeded {MAX_PROCESSES} processes")
            except (FileNotFoundError, ProcessLookupError):
                pass
            finally:
                for handle in captured:
                    handle.close()
        return self.identities()

    def descendants(self):
        return [handle for handle in self.handles.values() if handle is not self.root]

    def identities(self):
        return [dict(handle.identity) for handle in self.descendants()]

    def live(self, *, descendants_only=False):
        handles = self.descendants() if descendants_only else self.handles.values()
        return [handle for handle in handles if handle.live()]

    def signal(self, number, *, descendants_only=False):
        handles = self.descendants() if descendants_only else list(self.handles.values())
        # Stop the root first so it cannot keep creating new descendants.
        stopped, errors = 0, []
        for handle in handles:
            try:
                stopped += handle.send_signal(number)
            except BaseException as error:
                errors.append(type(error).__name__)
        if errors:
            raise RuntimeError("admitted pidfd signaling failed: " + "; ".join(errors))
        return stopped

    def close(self):
        # A caller may retry cleanup after an observation error. Remove only
        # confirmed exits before releasing descriptors; a closed live/unknown
        # identity must still fail observation rather than look safely retired.
        try:
            self.retire_exited()
        finally:
            for handle in self.handles.values():
                handle.close()


def consume_pending_signals(managed, old_mask):
    """Consume this runner's blocked termination signals before its commit."""
    pending = signal.sigpending() & (managed - old_mask)
    for number in pending:
        signal.sigwait({number})
    return pending
