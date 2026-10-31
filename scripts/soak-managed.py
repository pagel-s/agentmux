#!/usr/bin/env python3
"""Bounded no-model soak, restart and in-flight broker-crash validation."""
import argparse
import json
from pathlib import Path
import subprocess
import tempfile
import time

def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--binary',default='bin/agentmux');parser.add_argument('--turns',type=int,default=30);parser.add_argument('--interval',type=float,default=3);parser.add_argument('--output',required=True);args=parser.parse_args()
    if not 10<=args.turns<=1000 or not 0<=args.interval<=60: parser.error('turns10..1000, interval0..60')
    binary=str(Path(args.binary).resolve(strict=True));started=time.monotonic()
    with tempfile.TemporaryDirectory(prefix='amx-soak-') as root:
        state=Path(root)/'s';broker=None
        def cli(*parts,expected=0):
            p=subprocess.run([binary,'managed',*map(str,parts)],capture_output=True,text=True,timeout=30)
            if p.returncode!=expected: raise RuntimeError(p.stdout+' '+p.stderr)
            return json.loads(p.stdout).get('result')
        def call(op,body): return cli('call',op,'--credential-file',state/'owner.json','--json',json.dumps(body))
        script=r'''set -eu
input=$(cat)
case "$input" in *CRASH_ME*) printf STARTED > crash-started; sleep 30; printf UNEXPECTED >> crash-started;; esac
printf SOAK_OK
'''
        cfg={'version':2,'mode':'managed','session':'soak','agents':{'worker':{'harness':'command','sandbox':'bubblewrap','network':'none','command':['/bin/sh','-c',script],'trace':{'enabled':True,'max_turn_bytes':8192,'max_total_bytes':(args.turns+2)*8192}}},'limits':{'max_runtime_seconds':max(300,int(args.turns*(args.interval+2)+120)),'max_tasks':args.turns+10},'policy':{'default':'deny'}}
        path=Path(root)/'config.yaml';path.write_text(json.dumps(cfg));cli('init',path,'--state',state)
        def start():
            process=subprocess.Popen([binary,'managed','serve','--state',str(state),'--sandbox','bubblewrap'],stdout=subprocess.PIPE,stderr=subprocess.PIPE)
            deadline=time.monotonic()+10
            while time.monotonic()<deadline:
                if process.poll() is not None: raise RuntimeError(process.communicate()[1])
                try: call('identity',{});return process
                except (RuntimeError,FileNotFoundError): time.sleep(.03)
            raise RuntimeError('broker not ready')
        def stop(process,crash=False):
            process.kill() if crash else process.terminate()
            try: process.communicate(timeout=5)
            except subprocess.TimeoutExpired: process.kill();process.communicate()
        try:
            broker=start();deadline=None;restarts=0
            for i in range(args.turns):
                call('instruct',{'to':['worker'],'text':f'bounded soak turn {i}'})
                cli('worker','--state',state,'--agent','worker','--once')
                task=call('task.list',{'limit':200})['tasks'][-1]
                assert task['state']=='submitted' and task['result']=='SOAK_OK',task
                cli('trace','verify','--state',state,'--agent','worker','--id',task['trace']['id'],'--mac',task['trace']['mac'])
                call('task.verify',{'id':task['id'],'version':task['version']})
                agents=call('agent.list',{})['agents'];current=agents[0]['deadline']
                if deadline is None: deadline=current
                assert current==deadline,'restart extended session deadline'
                if (i+1)%5==0: stop(broker);broker=start();restarts+=1
                time.sleep(args.interval)
            # Read the new task via its authoritative owner list, avoiding any
            # assumption about the instruct response wrapper.
            call('instruct',{'to':['worker'],'text':'CRASH_ME'})
            worker=subprocess.Popen([binary,'managed','worker','--state',str(state),'--agent','worker','--once'],stdout=subprocess.PIPE,stderr=subprocess.PIPE)
            marker=state/'workspaces'/'worker'/'crash-started';until=time.monotonic()+10
            while not marker.exists():
                if worker.poll() is not None: raise RuntimeError('crash fixture ended early')
                if time.monotonic()>until: raise TimeoutError('crash fixture did not start')
                time.sleep(.03)
            stop(broker,crash=True);worker.communicate(timeout=5);broker=start()
            task=next(t for t in call('task.list',{'limit':200})['tasks'] if t['text']=='CRASH_ME')
            assert task['state']=='running' and task['delivery']=='uncertain',task
            cli('worker','--state',state,'--agent','worker','--once')
            assert marker.read_text()=='STARTED','crashed task was reexecuted'
            task=call('task.get',{'id':task['id']})['task'];call('task.resolve',{'id':task['id'],'version':task['version'],'resolution':'fail'})
            report={'ok':True,'turns_verified':args.turns,'clean_restarts':restarts,'seconds':round(time.monotonic()-started,3),'session_deadline_unchanged':True,'every_completed_trace_verified':True,'inflight_broker_crash':'uncertain, process stopped, no automatic replay, owner resolved fail','model_calls':0,'coverage':'bounded soak only; no multi-day availability claim'}
            print(json.dumps(report,indent=2))
            with open(args.output,'x') as f:json.dump(report,f,indent=2);f.write('\n')
        finally:
            if broker is not None and broker.poll() is None: stop(broker)

if __name__=='__main__':main()
