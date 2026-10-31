#!/usr/bin/env python3
"""Measure broker RPCs with 8/32/128 logical agents; no native/model calls.

Each iteration writes a board message, writes private CAS memory, then reads
the shared board. Latencies include authenticated HTTP/Unix and SQLite commit.
This measures a single broker, not 128 simultaneous model processes.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import http.client
import json
import os
from pathlib import Path
import platform
import socket
import subprocess
import tempfile
import time

class UnixHTTP(http.client.HTTPConnection):
    def __init__(self,path): super().__init__('localhost',timeout=30);self.path=path
    def connect(self):
        self.sock=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM);self.sock.settimeout(30);self.sock.connect(self.path)

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--binary',default='bin/agentmux');p.add_argument('--agents',default='8,32,128');p.add_argument('--iterations',type=int,default=20);p.add_argument('--output',required=True)
    a=p.parse_args();binary=str(Path(a.binary).resolve(strict=True));counts=[int(n) for n in a.agents.split(',')]
    if any(n<1 or n>512 for n in counts) or not 1<=a.iterations<=100: p.error('agents1..512, iterations1..100')
    results=[]
    for count in counts:
        with tempfile.TemporaryDirectory(prefix='amx-bench-') as root:
            state=Path(root)/'s';names=[f'agent{i}' for i in range(count)]
            cfg={'version':2,'mode':'managed','session':'broker-benchmark','agents':{n:{'harness':'command','command':['/bin/cat'],'sandbox':'bubblewrap','network':'none'} for n in names},
                 'boards':{'common':{'readers':['*'],'writers':['*']}},'policy':{'default':'deny'},
                 'limits':{'max_records':min(1000000,count*a.iterations*8+count*4+100),'max_requests':min(1000000,count*a.iterations*8+count*4+100),'max_messages':count*a.iterations+100,'max_runtime_seconds':600}}
            path=Path(root)/'c.yaml';path.write_text(json.dumps(cfg))
            subprocess.run([binary,'managed','init',str(path),'--state',str(state)],check=True,stdout=subprocess.DEVNULL)
            broker=subprocess.Popen([binary,'managed','serve','--state',str(state),'--sandbox','bubblewrap'],stdout=subprocess.PIPE,stderr=subprocess.PIPE)
            try:
                deadline=time.monotonic()+10
                while not (state/'broker.sock').exists():
                    if broker.poll() is not None: raise RuntimeError(broker.communicate()[1].decode())
                    if time.monotonic()>deadline: raise TimeoutError('broker not ready')
                    time.sleep(.02)
                credentials={name:json.loads((state/'credentials'/f'{name}.json').read_text()) for name in names}
                def actor(name):
                    http=UnixHTTP(str(state/'broker.sock'));latencies=[]
                    try:
                        for i in range(a.iterations):
                            calls=[('board.post',{'board':'common','text':f'{name}:{i}'}),('memory.put',{'namespace':'private','key':f'iteration{i}','value':f'{name}:{i}','version':0}),('message.list',{'board':'common','limit':10})]
                            for j,(op,args) in enumerate(calls):
                                body=json.dumps({'operation':op,'request_id':f'bench-{name}-{i}-{j}','args':args})
                                start=time.perf_counter_ns();http.request('POST','/v1/call',body,{'Authorization':'Bearer '+credentials[name]['token'],'Content-Type':'application/json'})
                                response=http.getresponse();value=json.loads(response.read());elapsed=(time.perf_counter_ns()-start)/1e6
                                if response.status!=200 or value.get('error'): raise RuntimeError(str(value.get('error')))
                                latencies.append(elapsed)
                        return latencies
                    finally: http.close()
                start=time.perf_counter()
                with ThreadPoolExecutor(max_workers=count) as pool: samples=[n for values in pool.map(actor,names) for n in values]
                seconds=time.perf_counter()-start;samples.sort()
                percentile=lambda q:samples[min(len(samples)-1,int((len(samples)-1)*q))]
                results.append({'logical_agents':count,'iterations_per_agent':a.iterations,'operations':len(samples),'seconds':round(seconds,4),'operations_per_second':round(len(samples)/seconds,2),'latency_ms':{'p50':round(percentile(.5),3),'p95':round(percentile(.95),3),'p99':round(percentile(.99),3),'max':round(samples[-1],3)},'errors':0})
                print(json.dumps(results[-1]),flush=True)
            finally:
                broker.terminate()
                try: broker.communicate(timeout=5)
                except subprocess.TimeoutExpired: broker.kill();broker.communicate()
    report={'workload':'board.post + private memory.put + board message.list per iteration','coverage':'single sandboxed SQLite broker, logical API agents; no model or worker-process scaling claim','machine':{'system':platform.system(),'arch':platform.machine(),'logical_cpus':os.cpu_count()},'results':results}
    target=Path(a.output)
    with target.open('x') as out: json.dump(report,out,indent=2);out.write('\n')

if __name__=='__main__': main()
