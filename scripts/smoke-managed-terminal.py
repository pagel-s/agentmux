#!/usr/bin/env python3
"""Model-free acceptance for native layouts, scratch shells and last attachment."""
import argparse
import json
import os
from pathlib import Path
import pty
import select
import shutil
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', default='bin/amx')
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve(strict=True))
    # A comma in a valid state path must not confuse TMUX's socket,pid,id tuple.
    root = Path(tempfile.mkdtemp(prefix='amx-terminal,-'))
    state = root / 'state'
    home = root / 'home'
    home.mkdir()
    env = {'PATH': os.environ['PATH'], 'HOME': str(home), 'SHELL': '/bin/sh',
           'TERM': 'xterm-256color', 'LC_ALL': 'C.UTF-8'}
    processes, masters = [], []
    started = False
    succeeded = False

    def command(*values):
        result = subprocess.run([binary, *map(str, values)], env=env,
                                text=True, capture_output=True, timeout=30)
        if result.returncode:
            raise AssertionError(result.stderr + result.stdout)
        return result.stdout

    def tmux(*values):
        return subprocess.check_output(['tmux', '-S', str(state / 'tmux.sock'),
                                        *map(str, values)], env=env, text=True,
                                       timeout=5).strip()

    def until(check, label):
        end = time.monotonic() + 15
        while time.monotonic() < end:
            for master in masters:
                try:
                    while select.select([master], [], [], 0)[0]:
                        os.read(master, 65536)
                except OSError:
                    pass
            result = check()
            if result:
                return result
            time.sleep(.06)
        raise AssertionError('Timed out: ' + label)

    def clients():
        return tmux('list-clients', '-F', '#{client_session}').splitlines()

    def attach(agent=None, selector=True):
        before = set(clients()) if processes else set()
        master, slave = pty.openpty()
        values = [binary, 'a'] + ([agent] if agent else []) + (['--state', str(state)] if selector else [])
        process = subprocess.Popen(values, env=env, stdin=slave, stdout=slave,
                                   stderr=slave, start_new_session=True)
        os.close(slave)
        masters.append(master)
        processes.append(process)
        view = until(lambda: next((v for v in clients() if v not in before), ''),
                     'new private attachment')
        return process, view, master

    try:
        agents = {}
        for actor in ['alice', 'bob']:
            project = root / ('project-' + actor)
            project.mkdir()
            agents[actor] = {'harness': 'command', 'interaction': 'interactive',
                             'sandbox': 'local', 'network': 'host',
                             'workdir': str(project),
                             'command': ['/bin/sh', '-c',
                                         'printf "READY:%s\\n" "$PWD"; while IFS= read -r line; do printf "INPUT:%s\\n" "$line"; done']}
        config = root / 'config.json'
        config.write_text(json.dumps({'version': 2, 'mode': 'managed',
                                      'session': 'terminal-uat', 'agents': agents,
                                      'limits': {'max_runtime_seconds': 180}}))
        runtime = json.loads(command('managed', 'up', config, '--state', state))['result']
        started = True
        command('workspaces', '--register', state)
        for actor in agents:
            command('label', actor, actor.title() + ' project', '--state', state)
        panes = {actor: record['pane_id'] for actor, record in runtime['agents'].items()}
        pids = {actor: tmux('display-message', '-p', '-t', pane, '#{pane_pid}')
                for actor, pane in panes.items()}
        for actor, pane in panes.items():
            until(lambda actor=actor, pane=pane:
                  'READY:' + str(root / ('project-' + actor)) in tmux('capture-pane', '-p', '-t', pane),
                  actor + ' starts in the selected project')
        first, view, master = attach('alice')
        command('split', 'alice', 'bob', '--state', state)
        window = tmux('display-message', '-p', '-t', panes['alice'], '#{window_id}')
        assert tmux('display-message', '-p', '-t', panes['bob'], '#{window_id}') == window
        assert tmux('display-message', '-p', '-t', view, '#{pane_id}') == panes['bob']
        peer, peer_view, _ = attach('alice')
        assert tmux('display-message', '-p', '-t', peer_view, '#{pane_id}') == panes['alice']
        command('unsplit', 'bob', '--state', state)
        assert tmux('display-message', '-p', '-t', panes['bob'], '#{window_id}') != window
        until(lambda: all(tmux('display-message', '-p', '-t', pane, '#{@agentmux_title}') == actor.title() + ' project [' + actor + ']'
                          for actor, pane in panes.items()), 'titles follow their exact agent after layout changes')
        # Use the actual default tmux split key: its cwd must follow Alice.
        tmux('select-window', '-t', view + ':' + tmux('display-message', '-p', '-t', panes['alice'], '#{window_index}'))
        tmux('select-pane', '-t', panes['alice'])
        before = set(tmux('list-panes', '-t', window, '-F', '#{pane_id}').splitlines())
        os.write(master, b'\x02%')
        helper = until(lambda: next((p for p in tmux('list-panes', '-t', window, '-F', '#{pane_id}').splitlines() if p not in before), ''), 'Ctrl-b % creates a scratch split')
        assert tmux('display-message', '-p', '-t', helper, '#{pane_current_path}') == str(root / 'project-alice')
        assert tmux('display-message', '-p', '-t', helper, '#{@agentmux_agent}') == ''
        os.write(master, b'\x02!')
        helper_window = until(lambda: (w if (w := tmux('display-message', '-p', '-t', helper, '#{window_id}')) != window else ''), 'Ctrl-b ! breaks scratch pane into its own window')
        until(lambda: helper_window in tmux('list-windows', '-t', runtime['session'], '-F', '#{window_id}').splitlines(), 'broken-out scratch pane anchored to workspace')
        # A user-created window must survive disposal of its private view.
        scratch = tmux('new-window', '-d', '-t', view, '-n', 'lookup', '-c', root / 'project-alice', '-P', '-F', '#{window_id}', 'exec sleep 120')
        until(lambda: scratch in tmux('list-windows', '-t', runtime['session'], '-F', '#{window_id}').splitlines(), 'scratch window anchored to workspace')
        tmux('select-window', '-t', view + ':' + tmux('display-message', '-p', '-t', panes['alice'], '#{window_index}'))
        tmux('select-pane', '-t', panes['alice'])
        until(lambda: tmux('show-options', '-v', '-t', runtime['session'], '@agentmux_last_agent') == 'alice', 'manual native focus remembered')
        tmux('detach-client', '-s', view)
        until(lambda: first.poll() is not None, 'normal detach cleans private view')
        assert scratch in tmux('list-windows', '-t', runtime['session'], '-F', '#{window_id}').splitlines()
        assert helper_window in tmux('list-windows', '-t', runtime['session'], '-F', '#{window_id}').splitlines()
        resumed, resumed_view, resumed_master = attach(selector=False)
        assert tmux('display-message', '-p', '-t', resumed_view, '#{pane_id}') == panes['alice']
        assert scratch in tmux('list-windows', '-t', resumed_view, '-F', '#{window_id}').splitlines()
        dashboard = tmux('show-options', '-v', '-t', resumed_view, '@agentmux_dashboard')
        rejected_env = dict(env)
        context = tmux('display-message', '-p', '-t', resumed_view, '#{socket_path},#{pid},#{session_id}').replace(',$', ',')
        rejected_env.update(TMUX=context, TMUX_PANE=dashboard)
        rejected = subprocess.run([binary, 'split', 'bob', '--state', str(state)], env=rejected_env, text=True, capture_output=True, timeout=10)
        assert rejected.returncode != 0 and 'dashboard' in rejected.stderr, rejected.stderr
        # A dashboard may be rearranged locally but must never become a shared
        # native/scratch window or leak into the lifecycle-owning core session.
        dashboard_window = tmux('display-message', '-p', '-t', dashboard, '#{window_id}')
        tmux('split-window', '-d', '-h', '-t', dashboard, 'exec sleep 120')
        tmux('select-window', '-t', resumed_view + ':' + tmux('display-message', '-p', '-t', dashboard, '#{window_index}'))
        tmux('select-pane', '-t', dashboard)
        os.write(resumed_master, b'\x02!')
        private_window = until(lambda: (w if (w := tmux('display-message', '-p', '-t', dashboard, '#{window_id}')) != dashboard_window else ''), 'dashboard pane breaks out locally')
        # The binding runs the guard in tmux's command queue after break-pane.
        time.sleep(.15)
        assert private_window not in tmux('list-windows', '-t', runtime['session'], '-F', '#{window_id}').splitlines()
        assert tmux('display-message', '-p', '-t', dashboard, '#{@agentmux_scratch}') == ''
        for actor, pane in panes.items():
            assert tmux('display-message', '-p', '-t', pane, '#{pane_pid}') == pids[actor]
            assert 'INPUT:' not in tmux('capture-pane', '-p', '-t', pane)
        print(json.dumps({'ok': True, 'model_calls': 0, 'checks': [
            'per-agent-project', 'combine-native-panes', 'exact-attach-in-shared-window',
            'unsplit-without-restart', 'exact-agent-titles', 'scratch-split-cwd', 'scratch-no-agent-identity',
            'scratch-breakout-survives-detach',
            'scratch-window-survives-detach', 'scratch-window-visible-on-reattach',
            'last-native-session-restored', 'dashboard-split-rejected',
            'dashboard-breakout-stays-private',
            'unchanged-native-pids', 'no-native-input-injection']}))
        succeeded = True
    finally:
        if not succeeded:
            try:
                for pane in tmux('list-panes', '-a', '-F', '#{pane_id}').splitlines():
                    (root / ('failure-' + pane.lstrip('%') + '.txt')).write_text(tmux('capture-pane', '-p', '-t', pane))
            except Exception:
                pass
            print('Private failure artifacts:', root)
        if started:
            try:
                command('managed', 'down', '--state', state)
            except Exception:
                pass
        for process in processes:
            if process.poll() is None:
                process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
        for master in masters:
            os.close(master)
        if succeeded:
            shutil.rmtree(root, ignore_errors=True)


if __name__ == '__main__':
    main()
