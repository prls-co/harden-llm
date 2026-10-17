import argparse, collections, datetime, hashlib, json, os, pathlib, re, shlex, subprocess, time
p=argparse.ArgumentParser()
p.add_argument('--live',action='store_true',required=True,help='explicitly authorize native CPA inference')
p.add_argument('--binary',default='/tmp/hllm-cpabench-minute-study')
p.add_argument('--output',required=True)
p.add_argument('--schedule',required=True,help='offsetSeconds:waveCount,...')
p.add_argument('--observe-for',type=float,default=0)
p.add_argument('--endpoint',choices=['public','loopback'],default='public')
a=p.parse_args()
assert a.live and pathlib.Path(a.binary).is_file()
schedule=[(float(t),int(n)) for t,n in (s.split(':') for s in a.schedule.split(','))]
assert schedule==sorted(schedule) and 1<=len(schedule)<=20
assert all(0<=t<=600 and 1<=n<=200 for t,n in schedule) and sum(n for t,n in schedule)<=2000
assert 0<=a.observe_for<=900
out=pathlib.Path(a.output);out.mkdir(mode=0o700)
env=os.environ.copy(); env.pop('OPENAI_API_KEY',None)
raw=pathlib.Path('/home/kirill/p/CLIProxyAPI-setup/.env').read_text()
match=re.search(r'^(?:export\s+)?CPA_API_KEY=(.*)$',raw,re.M)
assert match
values=shlex.split(match.group(1));assert len(values)==1
env['CPA_API_KEY']=values[0]
started=time.monotonic(); utc=datetime.datetime.now(datetime.timezone.utc).isoformat()
source=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()
metadata={'sourceSHA':source,'startedAt':utc,'schedule':schedule,'endpoint':a.endpoint,'observeForSeconds':a.observe_for,'callerRetries':False,'callTimeoutSeconds':180,'officialAPIEnabled':False,'sharedConfigurationChanged':False,'runnerSHA256':hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest()}
(out/'metadata.json').write_text(json.dumps(metadata,indent=2));os.chmod(out/'metadata.json',0o600)
jobs=[];idx=0;stop=False;last=-10

def snapshot():
    data=[];totals=collections.Counter(); finish_times=[]
    for job in jobs:
        events=job['dir']/'events.ndjson';ms=[]
        if events.exists():
            for line in events.read_text().splitlines():
                try:e=json.loads(line)
                except json.JSONDecodeError:continue
                if e['type']=='call_finished':ms.append(e['data']['measurement'])
        counts=collections.Counter(m['kind'] for m in ms);totals.update(counts)
        starts=[datetime.datetime.fromisoformat(m['startedAt'].replace('Z','+00:00')) for m in ms]
        finishes=[datetime.datetime.fromisoformat(m['finishedAt'].replace('Z','+00:00')) for m in ms]
        for m in ms:
            if m['kind']=='completed': finish_times.append((datetime.datetime.fromisoformat(m['finishedAt'].replace('Z','+00:00'))-datetime.datetime.fromisoformat(utc)).total_seconds())
        data.append({'wave':job['index'],'plannedOffsetSeconds':job['offset'],'launchOffsetSeconds':job['launched'],'count':job['count'],'exitCode':job['proc'].poll(),'outcomes':dict(counts),'activeOrPending':job['count']-len(ms),'firstRequestAt':min(starts).isoformat() if starts else None,'lastRequestAt':max(starts).isoformat() if starts else None,'lastResultAt':max(finishes).isoformat() if finishes else None,'maxCallSeconds':max([m['durationMs']/1000 for m in ms],default=0),'maxHeadersSeconds':max([m.get('headersMs',0)/1000 for m in ms],default=0),'retryAfterMs':sorted(set(m['retryAfterMs'] for m in ms))})
    finish_times.sort()
    return {'elapsedSeconds':time.monotonic()-started,'sourceSHA':source,'startedAt':utc,'plannedRequests':sum(n for t,n in schedule),'wavesLaunched':len(jobs),'outcomes':dict(totals),'waves':data,'nthCompletionSeconds':{str(n):finish_times[n-1] for n in [200,400,600,800,1000,1200] if len(finish_times)>=n},'allWorkersFinished':all(j['proc'].poll() is not None for j in jobs)}

try:
    while True:
        elapsed=time.monotonic()-started
        while idx<len(schedule) and elapsed>=schedule[idx][0] and not stop:
            offset,count=schedule[idx]; d=out/f'wave-{idx+1:02d}'
            log=(out/f'wave-{idx+1:02d}.log').open('x');os.chmod(log.name,0o600)
            cmd=[a.binary,'--live','--endpoint',a.endpoint,'--action','wave','--concurrency',str(count),'--call-timeout','180s','--duration','190s','--output',str(d)]
            proc=subprocess.Popen(cmd,env=env,stdout=log,stderr=log)
            jobs.append({'index':idx+1,'offset':offset,'count':count,'launched':time.monotonic()-started,'dir':d,'proc':proc,'log':log});idx+=1
            print(json.dumps({'event':'wave-launched','wave':idx,'count':count,'offsetSeconds':time.monotonic()-started}),flush=True)
        if elapsed-last>=5:
            s=snapshot();last=elapsed
            tmp=out/'status.json.tmp';tmp.write_text(json.dumps(s,indent=2));os.chmod(tmp,0o600);tmp.replace(out/'status.json')
            print(json.dumps({'event':'heartbeat','elapsedSeconds':round(elapsed,2),'waves':len(jobs),'outcomes':s['outcomes'],'runningProcesses':sum(j['proc'].poll() is None for j in jobs),'nextLaunchSeconds':schedule[idx][0] if idx<len(schedule) and not stop else None}),flush=True)
            if s['outcomes'].get('policy',0):stop=True
        if (idx==len(schedule) or stop) and all(j['proc'].poll() is not None for j in jobs) and elapsed>=a.observe_for:break
        if elapsed>max(a.observe_for,schedule[-1][0]+195):raise RuntimeError('study hard deadline')
        time.sleep(min(1,max(0.001,schedule[idx][0]-(time.monotonic()-started))) if idx<len(schedule) and not stop else 1)
finally:
    for j in jobs:
        if j['proc'].poll() is None:j['proc'].terminate()
    for j in jobs:
        try:j['proc'].wait(timeout=5)
        except subprocess.TimeoutExpired:j['proc'].kill();j['proc'].wait()
        j['log'].close()
    final=snapshot();final['state']='finished' if len(jobs)==len(schedule) and final['allWorkersFinished'] and sum(final['outcomes'].values())==final['plannedRequests'] else 'interrupted'
    (out/'summary.json').write_text(json.dumps(final,indent=2));os.chmod(out/'summary.json',0o600)
    tmp=out/'status.json.tmp';tmp.write_text(json.dumps(final,indent=2));os.chmod(tmp,0o600);tmp.replace(out/'status.json')
    print(json.dumps({'event':'finished','summary':final}),flush=True)
