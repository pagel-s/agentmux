"""Exercise the real Pi TUI against an in-process fake provider, never a model.

The only profile, broker, transcript, and terminal used are under the supplied
private test directory. Run through TestInstalledPiNativeTUIBridge.
"""
import json
import os
import pathlib
import pty
import select
import subprocess
import sys
import time

pi, package, extension, directory = sys.argv[1:]
root = pathlib.Path(directory)
(root / "profile").mkdir()
(root / "work").mkdir()
(root / "profile/settings.json").write_text(json.dumps({"defaultProjectTrust": "always", "quietStartup": True}))
compat = str(pathlib.Path(package) / "node_modules/@earendil-works/pi-ai/dist/compat.js")

broker = root / "broker.mjs"
broker.write_text("""#!/usr/bin/env node
import fs from 'node:fs';
const root = process.env.AGENTMUX_PI_TEST_ROOT;
const operation = process.argv[4];
let input = ''; for await (const part of process.stdin) input += part;
const args = JSON.parse(input);
fs.appendFileSync(root+'/broker.jsonl', JSON.stringify({operation,args})+'\\n');
let result = {};
if(operation === 'native.status') result={status:{mode:'automatic',cancel:false}};
if(operation === 'native.peek') result={candidate:fs.existsSync(root+'/claimed')?'':'candidate-1'};
if(operation === 'native.claim') {
  fs.writeFileSync(root+'/claimed','yes');
  result={delivery:{id:'delivery-1',task_id:'task-1',from:'fixture-peer',to:'pi',kind:'instruction',text:'Reply with a fixture stanza'}};
}
if(operation==='native.event' && args.phase==='completed') fs.writeFileSync(root+'/completed',JSON.stringify(args));
process.stdout.write(JSON.stringify({schema_version:1,kind:'managed',operation,ok:true,result}));
""")
broker.chmod(0o700)

provider = root / "provider.mjs"
provider.write_text("""
import fs from 'node:fs';
import {createAssistantMessageEventStream} from COMPAT;
const root=process.env.AGENTMUX_PI_TEST_ROOT;
const log=data=>fs.appendFileSync(root+'/native.jsonl',JSON.stringify(data)+'\\n');
export default function(pi) {
 pi.registerProvider('agentmux-fixture', {
  api:'agentmux-fixture-api',baseUrl:'http://127.0.0.1:1',apiKey:'no-network-fixture',
  models:[{id:'echo',name:'Fixture',reasoning:false,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:32000,maxTokens:1000}],
  streamSimple:(model,context)=>{
   log({event:'provider',messages:context.messages.filter(m=>m.role!=='system')});
   const stream=createAssistantMessageEventStream();
   queueMicrotask(()=>{
    const message={role:'assistant',content:[{type:'text',text:'A model-free fixture stanza'}],api:model.api,provider:model.provider,model:model.id,
     usage:{input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},stopReason:'stop',timestamp:Date.now()};
    stream.push({type:'start',partial:message}); stream.push({type:'done',reason:'stop',message}); stream.end();
   });
   return stream;
  }
 });
 let watcher,deadline;
 pi.on('session_start',(_event,ctx)=>{
  ctx.ui.setEditorText('Owner draft before delivery');
  log({event:'draft-created',draft:ctx.ui.getEditorText()});
  setTimeout(()=>{
   log({event:'draft-preserved',draft:ctx.ui.getEditorText()}); ctx.ui.setEditorText('');
  },1400);
  watcher=setInterval(()=>{if(fs.existsSync(root+'/completed')){clearInterval(watcher); clearTimeout(deadline);log({event:'done',draft:ctx.ui.getEditorText()});ctx.shutdown();}},100);
  deadline=setTimeout(()=>{log({event:'timeout'});ctx.shutdown();},10000);
 });
 pi.on('before_agent_start',(event,ctx)=>{
  log({event:'before-start',message:event.message});ctx.ui.setEditorText('Owner draft during peer turn');
 });
 pi.on('agent_settled',(_event,ctx)=>log({event:'settled',draft:ctx.ui.getEditorText()}));
 pi.on('session_shutdown',()=>{clearInterval(watcher);clearTimeout(deadline);});
}
""".replace("COMPAT", json.dumps(compat)))

master, slave = pty.openpty()
env = {**os.environ, "TERM": "xterm-256color", "PI_OFFLINE": "1", "PI_TELEMETRY": "0", "PI_SKIP_VERSION_CHECK": "1", "PI_CODING_AGENT_DIR": str(root / "profile"),
       "AGENTMUX_CLI": str(broker), "AGENTMUX_CREDENTIAL_FILE": str(root / "fixture-identity"),
       "AGENTMUX_SOCKET": str(root / "fixture-socket"), "AGENTMUX_PI_TEST_ROOT": str(root)}
cmd = [pi, "--no-extensions", "--extension", extension, "--extension", str(provider),
       "--no-session", "--model", "agentmux-fixture/echo"]
process = subprocess.Popen(cmd, cwd=root / "work", env=env, stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
os.close(slave)
output = bytearray()
try:
    deadline = time.monotonic() + 15
    while process.poll() is None and time.monotonic() < deadline:
        if select.select([master], [], [], 0.2)[0]:
            try:
                output.extend(os.read(master, 65536))
            except OSError:
                break
    if process.poll() is None:
        process.terminate()
    code = process.wait(timeout=5)
finally:
    os.close(master)
    (root / "terminal.bin").write_bytes(output)

native = [json.loads(line) for line in (root / "native.jsonl").read_text().splitlines()]
events = [json.loads(line) for line in (root / "broker.jsonl").read_text().splitlines()]
assert code == 0, (code, output[-2000:])
assert any(e["event"] == "draft-preserved" and e["draft"] == "Owner draft before delivery" for e in native), native
assert any(e["operation"] == "native.peek" and e["args"]["state"] == "draft" for e in events), events
assert len([e for e in native if e["event"] == "provider"]) == 1, native
provider_event = next(e for e in native if e["event"] == "provider")
assert provider_event["messages"][-1]["role"] == "user", provider_event
assert 'fixture-peer' in json.dumps(provider_event), provider_event
phases = [e["args"]["phase"] for e in events if e["operation"] == "native.event"]
assert phases == ["submitted", "running", "completed"], phases
reply = json.loads((root / "completed").read_text())
assert reply["result"] == "A model-free fixture stanza", reply
assert any(e["event"] == "settled" and e["draft"] == "Owner draft during peer turn" for e in native), native
assert native[-1] == {"event": "done", "draft": "Owner draft during peer turn"}, native
print("Real Pi native TUI: draft protection, user-role injection, settlement and broker reply passed")
