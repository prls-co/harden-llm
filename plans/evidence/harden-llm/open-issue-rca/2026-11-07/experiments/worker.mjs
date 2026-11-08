import fs from "node:fs";
const [root,id]=process.argv.slice(2);
fs.writeFileSync(root+"/"+id+".json",JSON.stringify({pid:process.pid,ppid:process.ppid}));
const wait=()=>fs.existsSync(root+"/release")?process.exit(0):setTimeout(wait,10);
wait();
