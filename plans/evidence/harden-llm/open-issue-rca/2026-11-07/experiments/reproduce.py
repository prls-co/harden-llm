import subprocess,time,os,json,signal
from pathlib import Path
root=Path(__file__).parent
parent=subprocess.Popen(["node",str(root/"parent.mjs"),str(root)],stdout=subprocess.DEVNULL,stderr=open(root/"parent.stderr","w"))
owned=[]
def identity(pid):
 try:
  s=Path(f"/proc/{pid}/stat").read_text().split(); return {"pid":pid,"state":s[2],"ppid":int(s[3]),"pgrp":int(s[4]),"start":s[21]}
 except FileNotFoundError: return None
try:
 deadline=time.monotonic()+10
 while not all((root/(x+".json")).exists() for x in "abc"):
  if time.monotonic()>deadline: raise RuntimeError("workers did not start")
  time.sleep(.01)
 for x in "abc":
  v=json.loads((root/(x+".json")).read_text()); owned.extend([identity(v["pid"]),identity(v["ppid"])])
 parent.send_signal(signal.SIGTERM); parent.wait(timeout=3)
 time.sleep(3.5)
 after=[identity(x["pid"]) for x in owned]
 report={"parent_exit":parent.returncode,"task_timeout_ms":3000,"wait_after_parent_exit_ms":3500,"owned_before":owned,"owned_after":after,"live_descendants_after_timeout":sum(x is not None and x["state"]!="Z" for x in after)}
finally:
 (root/"release").write_text("release")
 if parent.poll() is None: parent.terminate();parent.wait(timeout=3)
 time.sleep(.3)
 for x in owned:
  current=identity(x["pid"])
  if current and current["start"]==x["start"] and current["state"]!="Z": os.kill(x["pid"],signal.SIGTERM)
report["cleanup_live_descendants"]=sum((v:=identity(x["pid"])) is not None and v["state"]!="Z" for x in owned)
(root/"reproduction.json").write_text(json.dumps(report,indent=2)+"\n")
print(json.dumps(report,indent=2))
