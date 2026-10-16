import subprocess, sys, json, time, os, select
cwd=sys.argv[1]; cmd=sys.argv[2:]
H=os.environ['ACP_HOME']
env={'PATH':'/opt/node22/bin:/usr/bin:/bin','HOME':H,'CODEX_HOME':H+'/.codex','NO_BROWSER':'1'}
os.makedirs(H+'/.codex',exist_ok=True)
p=subprocess.Popen(cmd,cwd=cwd,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=open(H+'/stderr2.log','wb'),env=env)
req={"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{"fs":{"readTextFile":True,"writeTextFile":True},"terminal":True},"clientInfo":{"name":"p0-probe","version":"0"}}}
p.stdin.write((json.dumps(req)+"\n").encode()); p.stdin.flush()
end=time.time()+40
while time.time()<end:
    r,_,_=select.select([p.stdout],[],[],1)
    if r:
        l=p.stdout.readline()
        if not l: break
        print(l.decode()[:3000]); 
        if '"id":1' in l.decode().replace(' ',''): break
p.kill(); os._exit(0)
