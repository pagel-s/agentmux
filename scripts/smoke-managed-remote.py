#!/usr/bin/env python3
"""One isolated local and one real SSH worker; no model/provider calls."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--host',required=True)
    parser.add_argument('--remote-dir',required=True,help='new absolute directory, parent already present')
    parser.add_argument('--binary',default='bin/amx')
    parser.add_argument('--output')
    args=parser.parse_args()
    binary=str(Path(args.binary).resolve(strict=True))
    with tempfile.TemporaryDirectory(prefix='amx-remote-') as root:
        state=Path(root)/'s'
        def cli(*parts):
            p=subprocess.run([binary,'managed',*map(str,parts)],capture_output=True,text=True,timeout=60)
            if p.returncode: raise RuntimeError(p.stdout+' '+p.stderr)
            return json.loads(p.stdout).get('result')
        def call(op,body):
            return cli('call',op,'--credential-file',state/'owner.json','--json',json.dumps(body))
        lead=r'''set -eu
"$AGENTMUX_CLI" managed call board.post --json '{"board":"team","text":"local isolated lead"}' >/dev/null
"$AGENTMUX_CLI" managed call memory.put --json '{"namespace":"project","key":"handoff","value":"cross-machine-value","version":0}' >/dev/null
"$AGENTMUX_CLI" managed call instruct --json '{"to":["remote"],"text":"Check the shared board and memory; return REMOTE_OK."}' >/dev/null
printf LOCAL_OK
'''
        remote=r'''set -eu
test ! -e "$1/owner.json"
test ! -e "$1/credentials/lead.json"
"$AGENTMUX_CLI" managed call message.list --json '{"board":"team"}' | grep -q 'local isolated lead'
"$AGENTMUX_CLI" managed call memory.get --json '{"namespace":"project","key":"handoff"}' | grep -q cross-machine-value
if "$AGENTMUX_CLI" managed call instruct --json '{"to":["lead"],"text":"forbidden reverse command"}' >denied.json; then exit 11; fi
grep -q forbidden denied.json
"$AGENTMUX_CLI" managed call board.post --json '{"board":"team","text":"remote isolated worker"}' >/dev/null
printf REMOTE_OK
'''
        cfg={'version':2,'mode':'managed','session':'ssh-smoke','agents':{
            'lead':{'harness':'command','sandbox':'bubblewrap','network':'none','command':['/bin/sh','-c',lead],
                    'trace':{'enabled':True,'max_turn_bytes':32768,'max_total_bytes':65536}},
            'remote':{'harness':'command','sandbox':'bubblewrap','network':'none','command':['/bin/sh','-c',remote,'remote',args.remote_dir+'/state']}},
            'boards':{'team':{'readers':['*'],'writers':['*']}},
            'memory':{'project':{'readers':['*'],'writers':['lead']}},
            'policy':{'default':'deny','grants':[{'from':['lead'],'to':['remote'],'actions':['instruct']}]},
            'limits':{'max_runtime_seconds':180}}
        path=Path(root)/'c.yaml';path.write_text(json.dumps(cfg));cli('init',path,'--state',state)
        broker=subprocess.Popen([binary,'managed','serve','--state',str(state),'--sandbox','bubblewrap'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
        try:
            until=time.monotonic()+10
            while not (state/'broker.sock').exists():
                if broker.poll() is not None: raise RuntimeError('broker failed '+broker.communicate()[1])
                if time.monotonic()>until: raise RuntimeError('broker readiness timeout')
                time.sleep(.03)
            call('instruct',{'to':['lead'],'text':'Delegate once to remote.'})
            cli('worker','--state',state,'--agent','lead','--once')
            start=time.monotonic()
            p=subprocess.run([binary,'managed','remote','--state',str(state),'--agent','remote','--host',args.host,'--remote-dir',args.remote_dir,'--once'],capture_output=True,text=True,timeout=90)
            if p.returncode: raise RuntimeError('remote failed '+p.stdout+' '+p.stderr)
            # A second explicit task uses the exact existing deployment. The
            # central lease and remote worker lock still serialize execution.
            call('instruct',{'to':['remote'],'text':'Repeat the approved read-only checks in the same remote deployment.'})
            resumed=subprocess.run([binary,'managed','remote','--state',str(state),'--agent','remote','--host',args.host,'--remote-dir',args.remote_dir,'--once','--resume'],capture_output=True,text=True,timeout=90)
            if resumed.returncode: raise RuntimeError('remote resume failed '+resumed.stdout+' '+resumed.stderr)
            tasks=call('task.list',{})['tasks']
            assert {t['result'] for t in tasks}=={'LOCAL_OK','REMOTE_OK'},tasks
            assert all(t['state']=='submitted' for t in tasks),tasks
            leadtask=next(t for t in tasks if t['to']=='lead')
            trace=leadtask['trace']
            verified=cli('trace','verify','--state',state,'--agent','lead','--id',trace['id'],'--mac',trace['mac'])
            assert verified['complete']
            for task in tasks: call('task.verify',{'id':task['id'],'version':task['version']})
            result={'ok':True,'host':args.host,'seconds':round(time.monotonic()-start,3),'worker_machines':2,
                    'broker_sandbox':'bubblewrap','worker_sandboxes':2,'network':'none','ssh_unix_socket_forward':True,
                    'board_and_memory':True,'reverse_instruction_denied':True,'owner_peer_credentials_hidden':True,
                    'owner_verified_tasks':len(tasks),'local_trace_verified':True,'remote_resume_verified':True,'remote_trace':'explicitly unsupported without central collector',
                    'remote_deployment_retained':args.remote_dir}
            print(json.dumps(result,indent=2))
            if args.output: Path(args.output).write_text(json.dumps(result,indent=2)+'\n')
        finally:
            broker.terminate()
            try: broker.communicate(timeout=5)
            except subprocess.TimeoutExpired: broker.kill();broker.communicate()

if __name__=='__main__': main()
