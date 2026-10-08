import subprocess, sys, json, time, os, select
# usage: init.py <cwd> <cmd...>; sends ACP initialize, prints agentCapabilities + authMethods ids
cwd=sys.argv[1]; cmd=sys.argv[2:]
env={'PATH':'/opt/node22/bin:/usr/bin:/bin','HOME':os.environ['ACP_HOME'],'XDG_CONFIG_HOME':os.environ['ACP_HOME']+'/.config','XDG_DATA_HOME':os.environ['ACP_HOME']+'/.local/share','XDG_STATE_HOME':os.environ['ACP_HOME']+'/.local/state','XDG_CACHE_HOME':os.environ['ACP_HOME']+'/.cache','CODEX_HOME':os.environ['ACP_HOME']+'/.codex','NO_BROWSER':'1'}
p=subprocess.Popen(cmd,cwd=cwd,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,env=env)
req={"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{"fs":{"readTextFile":True,"writeTextFile":True},"terminal":True},"clientInfo":{"name":"p0-probe","version":"0"}}}
p.stdin.write((json.dumps(req)+"\n").encode()); p.stdin.flush()
end=time.time()+40; buf=b''
while time.time()<end:
    r,_,_=select.select([p.stdout],[],[],1)
    if r:
        l=p.stdout.readline()
        if not l: break
        try:
            m=json.loads(l)
        except Exception: print('non-json:',l[:200]); continue
        if m.get('id')==1:
            res=m.get('result',m)
            print(json.dumps({'agentInfo':res.get('agentInfo'),'protocolVersion':res.get('protocolVersion'),'agentCapabilities':res.get('agentCapabilities'),'authMethods':[(a.get('id'),a.get('type')) for a in res.get('authMethods',[])]},indent=1)[:3500]); break
        else: print('other msg:',str(m)[:200])
p.kill()
print('stderr:',p.stderr.read()[:500])
