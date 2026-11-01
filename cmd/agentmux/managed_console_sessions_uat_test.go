package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestConsoleSessionSwitcherRealTMux(t *testing.T) {
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
	cmd := exec.Command("python3", "-c", consoleSessionSwitcherUAT, binary)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real session-switcher UAT: %v\n%s", err, output)
	}
	t.Logf("%s", output)
}

const consoleSessionSwitcherUAT = `
import json,os,pty,shutil,subprocess,sys,tempfile,time
from pathlib import Path
binary=str(Path(sys.argv[1]).resolve(strict=True))
root=Path(tempfile.mkdtemp(prefix='amx-session-navigation-'))
alias=root/'amx';alias.symlink_to(binary)
state=root/'state'
env={'PATH':os.environ['PATH'],'HOME':str(root/'home'),'SHELL':'/bin/sh','TERM':'xterm-256color','LC_ALL':'C.UTF-8'}
(root/'home').mkdir()
started=False
def run(*args):
 p=subprocess.run([binary,*map(str,args)],env=env,text=True,capture_output=True,timeout=45)
 if p.returncode:raise RuntimeError(p.stderr+' '+p.stdout)
 return json.loads(p.stdout)
def tmux(*args):
 return subprocess.check_output(['tmux','-S',str(state/'tmux.sock'),*map(str,args)],env=env,text=True,timeout=5).strip()
def until(check,label):
 end=time.monotonic()+12
 while time.monotonic()<end:
  value=check()
  if value:return value
  time.sleep(.06)
 raise AssertionError('Timed out: '+label)
def screen():return tmux('capture-pane','-p','-t',dashboard)
def key(*args):tmux('send-keys','-t',dashboard,*args)
def text(value):tmux('send-keys','-t',dashboard,'-l',value)
def capture(name):
 value=screen();(root/(name+'.txt')).write_text(value);return value
def current_context(session):
 context=dict(env)
 socket,pid,session_id=tmux('display-message','-p','-t',session,'#{socket_path}|#{pid}|#{session_id}').split('|')
 context['TMUX']=','.join([socket,pid,session_id.removeprefix('$')])
 context['TMUX_PANE']=tmux('display-message','-p','-t',session,'#{pane_id}')
 return context
def current_list(session):
 p=subprocess.run([str(alias),'ls'],env=current_context(session),text=True,capture_output=True,timeout=5)
 assert p.returncode==0,(p.stdout,p.stderr)
 assert all('ID '+actor+' · command · workspace coordination active · worker running' in p.stdout for actor in agents),p.stdout
 assert all(str(root/('project-'+str(i))) in p.stdout for i in range(10)),p.stdout
try:
 agents={}
 for i in range(10):
  project=root/('project-'+str(i));project.mkdir()
  agents['session_%02d'%i]={'harness':'command','interaction':'interactive','sandbox':'local','network':'host','workdir':str(project),
   'command':['/bin/sh','-c','printf "READY\n"; while IFS= read -r line; do printf "INPUT:%s\n" "$line"; done']}
 config={'version':2,'mode':'managed','interactive_workspace':True,'session':'navigation-uat','agents':agents,'limits':{'max_runtime_seconds':90}}
 path=root/'config.json';path.write_text(json.dumps(config))
 runtime=run('managed','up',path,'--state',state)['result'];started=True
 dashboard=runtime['dashboard_pane']
 for i in range(10):
  run('managed','call','agent.label','--credential-file',state/'owner.json','--json',json.dumps({'id':'session_%02d'%i,'title':'Problem %02d'%i,'version':0}))
 tmux('resize-window','-t',dashboard,'-x','80','-y','28')
 until(lambda:'Problem 00' in screen(),'readable initial roster')
 pids={a:tmux('display-message','-p','-t',p['pane_id'],'#{pane_pid}') for a,p in runtime['agents'].items()}
 key('3');until(lambda:'SENDER' in screen(),'connections view')
 initial=capture('connections-80x28')
 assert 'SESSIONS' in initial and 'RECIPIENT' in initial and 'Ctrl+P' in initial,initial
 key('C-p');text('Problem 09')
 picker=until(lambda:(s if 'Switch session' in (s:=screen()) and 'session_09' in s else ''),'filtered full-width picker')
 assert '│' not in picker,picker
 (root/'switcher-80x28.txt').write_text(picker)
 key('Enter')
 until(lambda:tmux('display-message','-p','-t',runtime['session'],'#{pane_id}')==runtime['agents']['session_09']['pane_id'],'Enter focuses exact native terminal')
 tmux('select-window','-t',dashboard)
 until(lambda:'Session session_09 selected' in screen(),'neutral workbench return message')
 assert 'Native terminal selected.' not in screen()
 key('3');key('C-p');text('Problem 02')
 until(lambda:'session_02' in screen() and 'Switch session' in screen(),'second filtered selection')
 key('Tab')
 until(lambda:'SENDER' in screen() and 'Problem 02 [session_02]' in screen(),'Tab selects sender in current view')
 assert tmux('display-message','-p','-t',runtime['session'],'#{pane_id}')==dashboard
 tmux('resize-window','-t',dashboard,'-x','64','-y','24')
 until(lambda:'SESSIONS' in screen() and 'SENDER' in screen(),'narrow vertical sidebar')
 narrow=capture('connections-64x24')
 assert 'RECIPIENT' in narrow and '↑/↓' in narrow,narrow
 key('Right');until(lambda:'4 Memory' in screen() and 'Shared boundaries' in screen(),'global right view navigation')
 key('Left');until(lambda:'SENDER' in screen(),'global left view navigation')
 key('C-p');text('Problem 07');until(lambda:'session_07' in screen(),'cancelable session search');key('Escape')
 until(lambda:'Problem 02 [session_02]' in screen() and 'SENDER' in screen(),'Esc preserves sender')
 tmux('resize-window','-t',dashboard,'-x','50','-y','20')
 until(lambda:'Session 3/10' in screen(),'tiny selected-session control')
 tiny=capture('connections-50x20');assert 'Ctrl+P switch' in tiny and '│' not in tiny,tiny
 tmux('resize-window','-t',dashboard,'-x','80','-y','28')
 key('Enter')
 pane=runtime['agents']['session_02']['pane_id']
 until(lambda:tmux('display-message','-p','-t',runtime['session'],'#{pane_id}')==pane,'selected sender opens native')
 tmux('send-keys','-t',pane,'-l','OWNER_EXPLICIT_INPUT');tmux('send-keys','-t',pane,'Enter')
 until(lambda:'INPUT:OWNER_EXPLICIT_INPUT' in tmux('capture-pane','-p','-t',pane),'native interaction remains available')
 for actor,pane in runtime['agents'].items():
  assert tmux('display-message','-p','-t',pane['pane_id'],'#{pane_pid}')==pids[actor],'native process restarted'
 current_list(runtime['session'])
 # Real owned tmux view and unrelated session share this dedicated socket;
 # only the exact session with the current attempt tag may infer the workspace.
 tmux('new-session','-d','-t',runtime['session'],'-s','navigation-view')
 tmux('set-option','-t','navigation-view','@agentmux_view',runtime['attempt_id'])
 current_list('navigation-view')
 tmux('new-session','-d','-s','foreign-navigation','/bin/sh')
 foreign=subprocess.run([str(alias),'ls'],env=current_context('foreign-navigation'),text=True,capture_output=True,timeout=5)
 assert foreign.returncode!=0 and 'cannot verify the current Agentmux workspace' in foreign.stderr,(foreign.stdout,foreign.stderr)
 master,slave=pty.openpty()
 try:
  plain=subprocess.run([str(alias)],env=current_context(runtime['session']),stdin=slave,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,timeout=5)
  assert plain.returncode==0 and 'Current workspace: navigation-uat' in plain.stdout and '\x1b' not in plain.stdout,(plain.stdout,plain.stderr)
 finally:os.close(slave);os.close(master)
 result={'ok':True,'native_sessions':10,'model_calls':0,'checks':['vertical-roster-80','vertical-roster-64','tiny-session-control-50','sender-recipient-labels','ctrl-p-search','enter-native-focus','tab-select-here','esc-preserves-selection','global-left-right','native-input','unchanged-worker-pids','amx-owned-core-context','amx-owned-view-context','amx-rejects-foreign-session-same-socket','pty-input-piped-output-is-plain']}
 (root/'result.json').write_text(json.dumps(result,indent=2));print(json.dumps(result));print('Private artifacts:',root)
except Exception:
 if started:
  try:(root/'failure.txt').write_text(screen())
  except Exception:pass
 print('Private failure artifacts:',root);raise
finally:
 if started:
  try:run('managed','down','--state',state)
  except Exception:pass
 if os.environ.get('AGENTMUX_KEEP_UAT_ARTIFACTS')!='1':shutil.rmtree(root)
`
