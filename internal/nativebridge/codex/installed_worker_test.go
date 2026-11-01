package codex

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The managed worker must exercise production BuildInteractive/Wrap/native-run,
// rather than calling Run directly. Opt in with a freshly built Agentmux binary.
func TestInstalledCodexManagedWorkerProductionPath(t *testing.T) {
	if os.Getenv("AGENTMUX_CODEX_NATIVE_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_CODEX_NATIVE_INTEGRATION=1 for installed CLI contracts")
	}
	binary := os.Getenv("AGENTMUX_INTEGRATION_BINARY")
	if binary == "" {
		t.Skip("set AGENTMUX_INTEGRATION_BINARY to a freshly built agentmux binary")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(binary); err != nil || info.IsDir() {
		t.Fatal("Agentmux integration binary is unavailable")
	}
	codex, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "-c", managedCodexProductionFixture, binary, codex)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("production managed Codex contract failed: %v\n%s", err, output)
	}
	t.Log(string(output))
}

const managedCodexProductionFixture = `
import errno,fcntl,http.server,json,os,pathlib,pty,select,signal,stat,struct,subprocess,sys,tempfile,termios,threading,time
binary,codex=sys.argv[1:3]
root=pathlib.Path(tempfile.mkdtemp(prefix='amx-codex-prod-'))
state=root/'state';home=root/'codex-home';home.mkdir(mode=0o700)
model_inputs=[]
class API(http.server.BaseHTTPRequestHandler):
 def log_message(self,*a):pass
 def do_POST(self):
  body=json.loads(self.rfile.read(int(self.headers['Content-Length'])));model_inputs.append(json.dumps(body.get('input')))
  n=len(model_inputs);answer='PRODUCTION NATIVE FIXTURE ANSWER'
  msg={'id':f'msg_fixture_{n}','type':'message','role':'assistant','status':'completed','content':[{'type':'output_text','text':answer,'annotations':[]}]}
  response={'id':f'resp_fixture_{n}','object':'response','created_at':int(time.time()),'status':'completed','output':[msg],'usage':{'input_tokens':1,'output_tokens':1,'total_tokens':2,'input_tokens_details':{'cached_tokens':0},'output_tokens_details':{'reasoning_tokens':0}}}
  self.send_response(200);self.send_header('Content-Type','text/event-stream');self.end_headers()
  events=[{'type':'response.created','response':dict(response,status='in_progress',output=[])},{'type':'response.output_item.added','output_index':0,'item':dict(msg,status='in_progress',content=[])},{'type':'response.output_text.delta','item_id':msg['id'],'output_index':0,'content_index':0,'delta':answer},{'type':'response.output_item.done','output_index':0,'item':msg},{'type':'response.completed','response':response}]
  for event in events:self.wfile.write(('event: '+event['type']+'\ndata: '+json.dumps(event)+'\n\n').encode());self.wfile.flush()
api=http.server.HTTPServer(('127.0.0.1',0),API);threading.Thread(target=api.serve_forever,daemon=True).start()
(home/'config.toml').write_text(f'developer_instructions="OWNER_PRODUCTION_CONFIG_SENTINEL"\nmodel_provider="fixture"\nmodel="fixture-model"\n[model_providers.fixture]\nname="Synthetic local production fixture"\nbase_url="http://127.0.0.1:{api.server_port}/v1"\nwire_api="responses"\nrequires_openai_auth=false\nsupports_websockets=false\n[analytics]\nenabled=false\n')
launcher=root/'fixture-codex'
launcher.write_text('#!'+sys.executable+'\n'+'''import json,os,pathlib,stat,sys
path=os.environ.get('AGENTMUX_CODEX_CONTEXT_FILE','');record={'argv':sys.argv[1:],'context_file':bool(path),'context_private':False,'context_role':False,'context_outside_state':False}
if path:
 try:
  file=pathlib.Path(path);record.update(context_private=stat.S_ISREG(file.stat().st_mode) and stat.S_IMODE(file.stat().st_mode)==0o600,context_role='PRODUCTION_AGENTMUX_ROLE_SENTINEL'in file.read_text(),context_outside_state=not str(file.resolve()).startswith(os.environ['AMX_FIXTURE_STATE']+'/'))
 except OSError:pass
with open(os.environ['AMX_FIXTURE_LAUNCH_RECORD'],'a')as out:out.write(json.dumps(record)+'\\n')
os.execv(os.environ['AMX_FIXTURE_REAL_CODEX'],[os.environ['AMX_FIXTURE_REAL_CODEX']]+sys.argv[1:])
''');launcher.chmod(0o700)
env=dict(os.environ,CODEX_HOME=str(home),HOME=str(root),XDG_CONFIG_HOME=str(root),XDG_CACHE_HOME=str(root),TERM='xterm-256color',AMX_FIXTURE_STATE=str(state),AMX_FIXTURE_LAUNCH_RECORD=str(root/'launches.jsonl'),AMX_FIXTURE_REAL_CODEX=codex)
for key in ('AGENTMUX_CREDENTIAL_FILE','AGENTMUX_SOCKET','AGENTMUX_CODEX_CONTEXT','AGENTMUX_CODEX_CONTEXT_FILE'):env.pop(key,None)
config={'version':2,'mode':'managed','session':'codex-production-fixture','interactive_workspace':True,'agents':{'codex':{'harness':'codex','interaction':'interactive','sandbox':'local','network':'host','workdir':str(root),'role':'PRODUCTION_AGENTMUX_ROLE_SENTINEL','command':[str(launcher),'--ask-for-approval','never','--sandbox','read-only','-c','developer_instructions="OWNER_PRODUCTION_CONFIG_SENTINEL"'],'native_delivery':{'mode':'manual','max_turns':1,'max_hops':4,'results':True}}},'policy':{'default':'deny'},'limits':{'max_runtime_seconds':120}}
config_path=root/'team.json';config_path.write_text(json.dumps(config))
worker=broker=None;master=None;raw=bytearray();passed=False
stderr=open(root/'broker-stderr','w')
def cli(*args):
 p=subprocess.run([binary,*map(str,args)],capture_output=True,text=True,env=env,timeout=8)
 if p.returncode:raise RuntimeError('CLI '+str(args[:3])+' failed: '+p.stdout+' '+p.stderr)
 value=json.loads(p.stdout)
 if not value.get('ok'):raise RuntimeError('CLI result failed: '+str(value))
 return value['result']
def call(op,args):return cli('managed','call',op,'--credential-file',state/'owner.json','--json',json.dumps(args))
def drain(delay=.05):
 if master is None:return
 if select.select([master],[],[],delay)[0]:
  try:chunk=os.read(master,65536)
  except OSError as error:
   if error.errno==errno.EIO:return
   raise
  raw.extend(chunk)
  if b'\x1b[6n'in chunk:os.write(master,b'\x1b[1;1R')
try:
 cli('managed','init',config_path,'--state',state)
 broker=subprocess.Popen([binary,'managed','serve','--state',str(state)],env=env,stdout=subprocess.DEVNULL,stderr=stderr)
 deadline=time.time()+8
 while time.time()<deadline:
  try:call('identity',{});break
  except RuntimeError:time.sleep(.05)
 else:raise RuntimeError('broker never became ready')
 master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',35,110,0,0))
 def terminal_session():os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0)
 worker=subprocess.Popen([binary,'managed','worker','--state',str(state),'--agent','codex'],stdin=slave,stdout=slave,stderr=slave,env=env,preexec_fn=terminal_session);os.close(slave)
 deadline=time.time()+15;status={}
 while time.time()<deadline:
  drain();status=call('native.status',{'agent':'codex'})['status']
  if status.get('session') and status.get('connected') and b'Ask Codex to do anything'in raw:break
  if worker.poll() is not None:raise RuntimeError('managed native worker exited early: '+raw.decode(errors='replace'))
 else:raise RuntimeError('native registration or healthy TUI absent: '+str(status)+' '+raw.decode(errors='replace'))
 assert status['capabilities']['completion_evidence']=='native-events'
 os.write(master,b'MY UNSENT HUMAN DRAFT');time.sleep(.2);drain()
 task=call('instruct',{'to':['codex'],'text':'Produce the synthetic fixture answer for this authorized native instruction.'})['task']
 call('native.configure',{'agent':'codex','version':status['policy']['version'],'mode':'auto'})
 deadline=time.time()+15
 while time.time()<deadline:
  drain();task=call('task.get',{'id':task['id']})['task']
  if task.get('native',{}).get('phase')=='completed':break
  if worker.poll() is not None:raise RuntimeError('worker exited during native instruction')
 else:raise RuntimeError('production native instruction did not complete: '+str(task)+' '+raw.decode(errors='replace'))
 assert task['state']=='submitted' and task['delivery']=='acknowledged' and task['result']=='PRODUCTION NATIVE FIXTURE ANSWER',task
 assert task['native']['adapter']=='codex' and task['native']['evidence']=='native-events' and task['native'].get('native_ref'),task['native']
 deadline=time.time()+4
 while time.time()<deadline:
  drain();last=raw.rfind(b'PRODUCTION NATIVE FIXTURE ANSWER')
  if last>=0 and b'MY UNSENT HUMAN DRAFT'in raw[last:]:break
 else:raise RuntimeError('native answer/draft rendering failed: '+raw.decode(errors='replace'))
 assert model_inputs and all('MY UNSENT HUMAN DRAFT'not in value for value in model_inputs)
 peer=[value for value in model_inputs if any(item.get('role')=='user' and any(content.get('text','').startswith('[Agentmux Owner-authored instruction from') for content in item.get('content',[])if isinstance(content,dict))for item in json.loads(value)if isinstance(item,dict))]
 assert peer and all('OWNER_PRODUCTION_CONFIG_SENTINEL'in value and 'PRODUCTION_AGENTMUX_ROLE_SENTINEL'in value for value in peer),'production prompt lost owner or Agentmux instructions: '+str({'requests':len(model_inputs),'peers':len(peer),'owner':['OWNER_PRODUCTION_CONFIG_SENTINEL'in value for value in model_inputs],'role':['PRODUCTION_AGENTMUX_ROLE_SENTINEL'in value for value in model_inputs],'launches':(root/'launches.jsonl').read_text()})
 launches=[json.loads(line)for line in (root/'launches.jsonl').read_text().splitlines()]
 native=[record for record in launches if record['argv'] and(record['argv'][0]=='app-server'or'--remote'in record['argv'])]
 assert len(native)>=2 and all(record['context_file']and record['context_private']and record['context_role']and record['context_outside_state']for record in native),'production private context file was not used'
 for record in native:
  developer=[arg for arg in record['argv']if arg.startswith('developer_instructions=')]
  assert developer==['developer_instructions="OWNER_PRODUCTION_CONFIG_SENTINEL"'],'production wrapper erased owner override or retained replacement role override: '+str(record['argv'])
 print('Actual Agentmux CLI managed init/serve/worker: Auto native instruction completed with native-events evidence; owner config and Agentmux role coexist; private context file used; owner -c preserved; unsent human draft and PTY survive.')
 passed=True
finally:
 if master is not None:
  try:os.write(master,b'\x03\x03')
  except OSError:pass
 if worker is not None:
  try:worker.wait(timeout=3)
  except subprocess.TimeoutExpired:
   worker.terminate()
   try:worker.wait(timeout=3)
   except subprocess.TimeoutExpired:os.killpg(worker.pid,signal.SIGKILL);worker.wait()
 if master is not None:os.close(master)
 if broker is not None:
  broker.terminate()
  try:broker.wait(timeout=3)
  except subprocess.TimeoutExpired:broker.kill();broker.wait()
 stderr.close();api.shutdown()
 import shutil;shutil.rmtree(root)
`
