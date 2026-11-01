#!/usr/bin/env python3
"""Real broker + managed workers + two native Pi TUIs; offline fake provider.

No user profile or model endpoint is used. All tmux processes, credentials,
projects and transcripts belong to this disposable acceptance workspace.
"""
import argparse
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import tempfile
import time


PROVIDER = r'''
import fs from "node:fs";
import {createAssistantMessageEventStream} from COMPAT;
const root=process.env.AGENTMUX_PI_FIXTURE_DIR;
const hold=process.env.AGENTMUX_PI_FIXTURE_DRAFT==='1';
const log=data=>fs.appendFileSync(root+'/native.jsonl',JSON.stringify(data)+'\n');
export default function(pi){
 pi.registerProvider('agentmux-fixture',{
  api:'agentmux-fixture-api',baseUrl:'http://127.0.0.1:1',apiKey:'no-network-fixture',
  models:[{id:'echo',name:'Fixture',reasoning:false,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:32000,maxTokens:1000}],
  streamSimple:(model,context)=>{
   const last=context.messages.filter(m=>m.role==='user').at(-1);
   log({event:'provider',message:last});
   const stream=createAssistantMessageEventStream();
   const finish=()=>{
    if(fs.existsSync(root+'/hold-turn') && JSON.stringify(last).includes(fs.readFileSync(root+'/hold-turn','utf8'))){setTimeout(finish,50);return;}
    const text=JSON.stringify(last).includes('PRIVATE_OWNER_FOLLOWUP_TOKEN')?'PRIVATE_OWNER_OUTPUT_TOKEN':'MODEL_FREE_STANZA from '+process.env.AGENTMUX_PI_FIXTURE_AGENT;
    const answer={role:'assistant',content:[{type:'text',text}],api:model.api,provider:model.provider,model:model.id,
     usage:{input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},stopReason:'stop',timestamp:Date.now()};
    stream.push({type:'start',partial:answer});stream.push({type:'done',reason:'stop',message:answer});stream.end();
   };
   setTimeout(finish,200);
   return stream;
  }
 });
 let watcher,lastRelease='';
 pi.on('session_start',(_event,ctx)=>{
  if(hold)ctx.ui.setEditorText('Owner draft before collaboration');
  log({event:'ready',draft:ctx.ui.getEditorText()});
  watcher=setInterval(()=>{
   if(!fs.existsSync(root+'/release'))return;
   const release=fs.readFileSync(root+'/release','utf8');
   if(release!==lastRelease){lastRelease=release;log({event:'draft-before-clear',draft:ctx.ui.getEditorText()});ctx.ui.setEditorText('');}
  },50);
 });
 pi.on('before_agent_start',(event,ctx)=>{
  log({event:'before-start',details:event.message?.details,text:event.message?.content});
  if(hold)ctx.ui.setEditorText('Owner draft during collaboration');
 });
 pi.on('agent_settled',(_event,ctx)=>log({event:'settled',draft:ctx.ui.getEditorText()}));
 pi.on('input',event=>log({event:'input',source:event.source,text:event.text}));
 pi.on('session_shutdown',()=>clearInterval(watcher));
}
'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True)
    parser.add_argument('--sandbox', choices=('local', 'bubblewrap'), default='local')
    parser.add_argument('--keep-output', action='store_true')
    parser.add_argument('--recovery', action='store_true',help='Kill only the broker during an active native turn, resolve uncertainty, then run new work')
    options = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix='amx-pi-native-'))
    binary = root / 'agentmux'
    shutil.copy2(Path(options.binary).resolve(strict=True), binary)
    binary.chmod(0o700)
    pi = Path(shutil.which('pi')).resolve(strict=True)
    node = Path(shutil.which('node')).resolve(strict=True)
    package = pi.parent.parent.parent
    compat = str(package / 'node_modules/@earendil-works/pi-ai/dist/compat.js')
    state = root / 'state'
    config_path = root / 'config.json'
    actors = ('alice', 'bob')
    agents = {}
    started = False
    last = None

    def run(*args, allowed=False):
        p = subprocess.run([str(binary), *map(str,args)], capture_output=True, text=True, timeout=35)
        if p.returncode and not allowed:
            raise RuntimeError(f'{args[:3]} failed: {p.stdout} {p.stderr}')
        return json.loads(p.stdout)

    def call(op, args=None, actor=None, allowed=False):
        credential = state / ('owner.json' if actor is None else f'credentials/{actor}.json')
        return run('managed','call',op,'--credential-file',credential,'--json',json.dumps(args or {}),allowed=allowed)

    def status(actor):
        return call('native.status',{'agent':actor})['result']['status']

    def configure(actor, mode, **more):
        return call('native.configure',{'agent':actor,'version':status(actor)['policy']['version'],'mode':mode,**more})['result']

    def until(predicate, label, timeout=20):
        nonlocal last
        deadline=time.monotonic()+timeout
        while time.monotonic()<deadline:
            last=predicate()
            if last:return last
            time.sleep(.1)
        raise AssertionError(f'Timed out: {label}; last={last}')

    def records(actor):
        path=root/actor/'native.jsonl'
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def release(actor):
        (root/actor/'release').write_text(str(time.monotonic_ns()))

    def task(task_id):
        return call('task.get',{'id':task_id})['result']['task']

    def running(task_id):
        current=task(task_id)
        if current.get('native',{}).get('phase')=='running':return current
        # Only this acceptance fixture writes these synthetic drafts. Boundary
        # notices may run ahead of the requested turn and create another one.
        release('bob')
        return None

    def tmux(*args):
        return subprocess.check_output(['tmux','-S',str(state/'tmux.sock'),*args],text=True,timeout=5)

    try:
        for actor in actors:
            project=root/actor
            project.mkdir()
            (project/'profile').mkdir()
            (project/'profile/settings.json').write_text(json.dumps({'defaultProjectTrust':'always','quietStartup':True}))
            (project/'provider.mjs').write_text(PROVIDER.replace('COMPAT',json.dumps(compat)))
            work=str(project) if options.sandbox=='local' else '/workspace'
            # Native-harness preflight requires host networking. PI_OFFLINE
            # and the in-process fixture provider prevent actual HTTP calls.
            agents[actor]={'harness':'pi','interaction':'interactive','sandbox':options.sandbox,'network':'host','workdir':str(project),
                'command':[str(node),str(pi),'--no-extensions','--extension',work+'/provider.mjs','--no-session'],
                'model':'agentmux-fixture/echo','read_only_paths':[str(node),str(package)],
                'env':{'PI_OFFLINE':'1','PI_TELEMETRY':'0','PI_SKIP_VERSION_CHECK':'1','PI_CODING_AGENT_DIR':work+'/profile',
                    'AGENTMUX_PI_FIXTURE_DIR':work,'AGENTMUX_PI_FIXTURE_AGENT':actor,'AGENTMUX_PI_FIXTURE_DRAFT':'1' if actor=='bob' else '0'}}
        config={'version':2,'mode':'managed','session':'pi-native-'+options.sandbox,'interactive_workspace':True,'agents':agents,
            'policy':{'default':'deny','grants':[{'from':['alice'],'to':['bob'],'actions':['instruct','message.send']}]},
            'limits':{'max_runtime_seconds':90,'max_tasks':30,'max_requests':10000,'max_records':10000}}
        config_path.write_text(json.dumps(config))
        runtime=run('managed','up',config_path,'--state',state,'--broker-sandbox',options.sandbox)['result']
        started=True
        for actor in actors:
            until(lambda actor=actor: status(actor).get('connected') and status(actor).get('adapter')=='pi',f'{actor} native registration')
        pids={actor:tmux('display-message','-p','-t',runtime['agents'][actor]['pane_id'],'#{pane_pid}').strip() for actor in actors}
        initial=call('instruct',{'to':['bob'],'text':'REVIEW_NATIVE_STANZA: write a fixture stanza'},actor='alice')['result']['task']
        time.sleep(1)
        assert not any(e['event']=='provider' for e in records('bob')), 'Manual mode started a turn'
        assert task(initial['id'])['delivery']=='pending'
        configure('bob','review',max_hops=16)
        st=status('bob')
        assert any(d['id']==initial['id'] for d in st['pending'])
        call('native.approve',{'agent':'bob','candidate':initial['id'],'version':st['policy']['version']})
        time.sleep(1)
        assert not any(e['event']=='provider' for e in records('bob')), 'Approved turn overwrote a human draft'
        assert any(e['event']=='ready' and e['draft']=='Owner draft before collaboration' for e in records('bob'))
        release('bob')
        completed=until(lambda: (t if (t:=task(initial['id']))['state']=='submitted' else None),'reviewed peer instruction completion')
        assert completed['native']['phase']=='completed' and completed['result']=='MODEL_FREE_STANZA from bob',completed
        assert completed['native']['evidence']=='native-events',completed
        assert any(e['event']=='settled' and e['draft']=='Owner draft during collaboration' for e in records('bob'))
        denied=call('instruct',{'to':['alice'],'text':'REVERSE_COMMAND_MUST_BE_DENIED'},actor='bob',allowed=True)
        assert not denied['ok'] and denied['error']['code']=='forbidden',denied
        configure('alice','auto',results=True,max_hops=16)
        result_message=until(lambda: next((e for e in records('alice') if e['event']=='before-start' and 'Result of your instruction '+initial['id'] in e.get('text','')),None),'request-scoped result return')
        assert 'Observation, not an instruction grant' in result_message['text'],result_message
        configure('bob','manual')
        later=call('instruct',{'to':['bob'],'text':'AUTO_NATIVE_STANZA: second fixture stanza'},actor='alice')['result']['task']
        release('bob')
        before=len([e for e in records('bob') if e['event']=='provider'])
        time.sleep(1)
        assert len([e for e in records('bob') if e['event']=='provider'])==before,'Manual pause admitted a turn'
        configure('bob','auto')
        completed=until(lambda: (t if (t:=task(later['id']))['state']=='submitted' else None),'automatic peer instruction completion')
        assert completed['native']['phase']=='completed',completed
        (root/'bob/hold-turn').write_text('HUMAN_INTERVENTION_STANZA')
        intervened=call('instruct',{'to':['bob'],'text':'HUMAN_INTERVENTION_STANZA: held fixture turn'},actor='alice')['result']['task']
        release('bob')
        until(lambda: running(intervened['id']),'human intervention fixture running')
        cleared_before=len([e for e in records('bob') if e['event']=='draft-before-clear'])
        release('bob')
        until(lambda: len([e for e in records('bob') if e['event']=='draft-before-clear'])>cleared_before,'native human draft cleared by fixture')
        tmux('send-keys','-t',runtime['agents']['bob']['pane_id'],'-l','PRIVATE_OWNER_FOLLOWUP_TOKEN')
        tmux('send-keys','-t',runtime['agents']['bob']['pane_id'],'Enter')
        until(lambda: any(e['event']=='input' and e.get('source')=='interactive' and 'PRIVATE_OWNER_FOLLOWUP_TOKEN' in e.get('text','') for e in records('bob')),'actual native interactive input hook')
        (root/'bob/hold-turn').unlink()
        withheld=until(lambda: (t if (t:=task(intervened['id']))['state']=='submitted' else None),'intervened native settlement')
        assert withheld['native']['phase']=='completed' and 'withheld' in withheld['result'],withheld
        assert 'PRIVATE_OWNER' not in json.dumps(withheld), 'Human follow-up leaked into broker result'
        assert any(e['event']=='provider' and 'PRIVATE_OWNER_FOLLOWUP_TOKEN' in json.dumps(e) for e in records('bob')),'Human follow-up was not actually queued into native work'
        returned=until(lambda: next((e for e in records('alice') if e['event']=='before-start' and 'Result of your instruction '+intervened['id'] in e.get('text','')),None),'withheld-result notice returned to requester')
        assert 'withheld' in returned['text'] and 'PRIVATE_OWNER' not in returned['text'],returned
        if options.recovery:
            (root/'bob/hold-turn').write_text('BROKER_RECOVERY_STANZA')
            interrupted=call('instruct',{'to':['bob'],'text':'BROKER_RECOVERY_STANZA: held fixture turn'},actor='alice')['result']['task']
            release('bob')
            until(lambda: running(interrupted['id']),'held native turn running')
            old_broker=tmux('display-message','-p','-t',runtime['broker_pane'],'#{pane_pid}').strip()
            os.kill(int(old_broker),signal.SIGKILL)
            def broker_restored():
                try:
                    manifest=json.loads((state/'runtime.json').read_text())
                    current=tmux('display-message','-p','-t',runtime['broker_pane'],'#{pane_pid}').strip()
                    return manifest['state']=='running' and current!=old_broker and status('bob')
                except (RuntimeError,FileNotFoundError,json.JSONDecodeError):return False
            until(broker_restored,'broker-only automatic recovery')
            uncertain=task(interrupted['id'])
            assert uncertain['delivery']=='uncertain',uncertain
            settled_before=len([e for e in records('bob') if e['event']=='settled'])
            (root/'bob/hold-turn').unlink()
            until(lambda: len([e for e in records('bob') if e['event']=='settled'])>settled_before,'old native turn settled after recovery')
            time.sleep(1)
            uncertain=task(interrupted['id'])
            assert uncertain['delivery']=='uncertain', 'Unaccepted stale native report resolved a fenced task'
            call('task.resolve',{'id':uncertain['id'],'version':uncertain['version'],'resolution':'fail'})
            replacement=call('instruct',{'to':['bob'],'text':'AFTER_RECOVERY_STANZA: new explicitly requested work'},actor='alice')['result']['task']
            release('bob')
            until(lambda: task(replacement['id'])['state']=='submitted','new native work after explicit uncertainty resolution')
            received=[e for e in records('bob') if e['event']=='before-start' and 'BROKER_RECOVERY_STANZA' in e.get('text','')]
            assert len(received)==1,'Uncertain native instruction was automatically replayed'
        for actor in actors:
            current=tmux('display-message','-p','-t',runtime['agents'][actor]['pane_id'],'#{pane_pid}').strip()
            assert current==pids[actor],f'{actor} native worker was restarted'
        result={'ok':True,'sandbox':options.sandbox,'real_pi_tuis':2,'model_calls':0,'checks':['manual','review','draft-protection','attributed-native-user-turn','settled-completion','native-event-evidence','request-scoped-result','reverse-command-denied','auto','human-follow-up-result-withheld','stable-worker-pids']}
        if options.recovery:result['checks']+=['broker-only-restart','uncertain-task-fencing','owner-resolution','new-work-after-recovery','no-turn-replay']
        (root/'result.json').write_text(json.dumps(result,indent=2))
        print(json.dumps(result))
    except Exception:
        if started:
            try:
                diagnostics={actor:{'native':status(actor),'terminal':tmux('capture-pane','-p','-t',runtime['agents'][actor]['pane_id'],'-S','-40')} for actor in actors}
                (root/'failure.json').write_text(json.dumps(diagnostics,indent=2))
            except Exception:pass
        raise
    finally:
        if started:
            try:run('managed','down','--state',state)
            except Exception:pass
        if options.keep_output:print('Private fixture artifacts:',root)
        else:shutil.rmtree(root)


if __name__=='__main__':main()
