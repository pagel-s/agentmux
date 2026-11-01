package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestConsoleWorkspaceHandoffRealTMux(t *testing.T) {
	if os.Getenv("AGENTMUX_MANAGED_INTEGRATION") != "1" {
		t.Skip("requires explicit disposable tmux integration")
	}
	binary := os.Getenv("AGENTMUX_TEST_BINARY")
	if binary == "" {
		binary = "../../bin/agentmux"
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("python3", "-c", consoleHandoffUAT, binary).CombinedOutput()
	if err != nil {
		t.Fatalf("workspace handoff UAT: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}

const consoleHandoffUAT = `
import json,os,pty,select,shutil,subprocess,sys,tempfile,time
from pathlib import Path
binary=str(Path(sys.argv[1]).resolve(strict=True))
root=Path(tempfile.mkdtemp(prefix='amx-handoff-uat-'))
env={'PATH':os.environ['PATH'],'HOME':str(root/'home'),'SHELL':'/bin/sh','TERM':'xterm-256color','LC_ALL':'C.UTF-8'}
(root/'home').mkdir();states=[root/'a',root/'b'];processes=[];masters=[]
def command(*args):
 p=subprocess.run([binary,*map(str,args)],env=env,text=True,capture_output=True,timeout=30)
 if p.returncode:raise RuntimeError(p.stderr+' '+p.stdout)
 return p.stdout
def call(*args):return json.loads(command(*args))
def tmux(state,*args):return subprocess.check_output(['tmux','-S',str(state/'tmux.sock'),*map(str,args)],env=env,text=True,timeout=5).strip()
def drain():
 for m in masters:
  try:
   while select.select([m],[],[],0)[0]:os.read(m,65536)
  except OSError:pass
def until(check,label):
 end=time.monotonic()+15
 while time.monotonic()<end:
  drain()
  value=check()
  if value:return value
  time.sleep(.07)
 raise AssertionError('Timed out: '+label)
def views(state):
 try:return [r.split('\t')[0] for r in tmux(state,'list-sessions','-F','#{session_name}\t#{@agentmux_view}').splitlines() if r.startswith('view_')]
 except subprocess.CalledProcessError:return []
def pane(state,view):
 try:return tmux(state,'show-options','-v','-t',view,'@agentmux_dashboard')
 except subprocess.CalledProcessError:return '' 
def screen(state,dashboard):return tmux(state,'capture-pane','-p','-t',dashboard)
def key(state,dashboard,*keys):tmux(state,'send-keys','-t',dashboard,*keys)
def text(state,dashboard,value):tmux(state,'send-keys','-t',dashboard,'-l','--',value)
def attach(state,agent):
 m,s=pty.openpty();p=subprocess.Popen([binary,'attach',agent,'--state',str(state)],env=env,stdin=s,stdout=s,stderr=s,start_new_session=True);os.close(s);masters.append(m);processes.append(p);return p
try:
 runtimes=[]
 for i,state in enumerate(states):
  project=root/('project-'+str(i));project.mkdir()
  cfg={'version':2,'mode':'managed','interactive_workspace':True,'session':'handoff-'+('a' if i==0 else 'b'),'agents':{},'limits':{'max_runtime_seconds':120}}
  for actor in ['alice','bob']:
   cfg['agents'][actor]={'harness':'command','interaction':'interactive','sandbox':'local','network':'host','workdir':str(project),'command':['/bin/sh','-c','printf "READY\n"; while IFS= read -r line; do printf "INPUT:%s\n" "$line"; done']}
  cfgpath=root/('config-'+str(i)+'.json');cfgpath.write_text(json.dumps(cfg));runtimes.append(call('managed','up',cfgpath,'--state',state)['result'])
  command('workspaces','--register',state)
  for actor in ['alice','bob']:call('managed','call','agent.label','--credential-file',state/'owner.json','--json',json.dumps({'id':actor,'title':actor.title()+' task','version':0}))
 first=attach(states[0],'bob');view_a=until(lambda:(v[0] if (v:=views(states[0])) else ''),'first private view')
 dash_a=until(lambda:pane(states[0],view_a),'first dashboard initialized')
 until(lambda:view_a in tmux(states[0],'list-clients','-F','#{client_session}').splitlines(),'first client attached')
 peer=attach(states[0],'alice');peer_view=until(lambda:next((v for v in views(states[0]) if v!=view_a),''),'second independent client')
 until(lambda:peer_view in tmux(states[0],'list-clients','-F','#{client_session}').splitlines(),'peer client attached')
 peer_before=tmux(states[0],'display-message','-p','-t',peer_view,'#{pane_id}')
 # Returning from a direct Bob attachment must select Bob in its dashboard.
 tmux(states[0],'select-window','-t',dash_a)
 key(states[0],dash_a,'2')
 until(lambda:'Bob task [bob]' in screen(states[0],dash_a),'direct attachment reconciles exact dashboard selection')
 # A native focus observed during a picker must lose to its explicit choice.
 key(states[0],dash_a,'C-p');until(lambda:'Switch session' in screen(states[0],dash_a),'picker before deferred focus')
 tmux(states[0],'select-window','-t',view_a+':agent-alice');tmux(states[0],'select-window','-t',dash_a)
 until(lambda:tmux(states[0],'show-options','-v','-t',view_a,'@agentmux_focus_pending')=='','pending native focus consumed during picker')
 text(states[0],dash_a,'bob');key(states[0],dash_a,'Tab');time.sleep(1.2)
 assert 'Bob task [bob]' in screen(states[0],dash_a),'deferred native Alice focus overrode explicit Bob choice'
 key(states[0],dash_a,'C-p');text(states[0],dash_a,'alice');key(states[0],dash_a,'Tab')
 until(lambda:'Alice task [alice]' in screen(states[0],dash_a),'explicit Alice choice after picker')
 tmux(states[0],'select-window','-t',view_a+':agent-bob');tmux(states[0],'select-window','-t',dash_a)
 until(lambda:'Bob task [bob]' in screen(states[0],dash_a),'new native focus honored after explicit choice revision')
 key(states[0],dash_a,'s');text(states[0],dash_a,'DRAFT-LEFT-UNSENT')
 until(lambda:'DRAFT-LEFT-UNSENT' in screen(states[0],dash_a),'unsent draft')
 key(states[0],dash_a,'C-w');until(lambda:'Workspaces' in screen(states[0],dash_a),'workspace chooser above retained draft')
 key(states[0],dash_a,'Down','Enter')
 view_b=until(lambda:(v[0] if (v:=views(states[1])) else ''),'interactive target workspace')
 dash_b=until(lambda:pane(states[1],view_b),'target dashboard initialized')
 until(lambda:tmux(states[1],'list-clients','-F','#{client_session}')==view_b,'foreground client moved across dedicated servers')
 assert view_a in views(states[0]),'old private dashboard was destroyed'
 assert tmux(states[0],'display-message','-p','-t',peer_view,'#{pane_id}')==peer_before,'handoff moved another client'
 key(states[1],dash_b,'C-w');until(lambda:'Workspaces' in screen(states[1],dash_b),'return workspace chooser')
 key(states[1],dash_b,'Enter')
 until(lambda:view_a in tmux(states[0],'list-clients','-F','#{client_session}').splitlines(),'reattach cached previous view')
 until(lambda:'DRAFT-LEFT-UNSENT' in screen(states[0],dash_a),'draft survived interactive handoff and return')
 text(states[0],dash_a,'-TAIL')
 until(lambda:'DRAFT-LEFT-UNSENT-TAIL' in screen(states[0],dash_a),'restored draft remains editable')
 for state,runtime in zip(states,runtimes):
  for a,p in runtime['agents'].items():assert 'INPUT:' not in tmux(state,'capture-pane','-p','-t',p['pane_id']),'handoff typed into native agent'
 key(states[0],dash_a,'C-c');until(lambda:first.poll() is not None,'normal detach finishes outer loop')
 assert first.returncode==0
 assert peer_view in views(states[0]),'normal cleanup removed peer view'
 assert view_a not in views(states[0]) and view_b not in views(states[1]),'outer attachment leaked cached private views'
 print('PASS: cross-server interactive handoff retained unsent editable draft, exact Bob focus and selection revision fencing, independent peer client and all native inputs; cached views cleaned on final detach')
finally:
 for p in processes:
  if p.poll() is None:p.terminate()
 for state in states:
  try:command('managed','down','--state',state)
  except Exception:pass
 for m in masters:
  try:os.close(m)
  except OSError:pass
 shutil.rmtree(root,ignore_errors=True)
`
