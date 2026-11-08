import {runTasks} from "file:///home/kirill/p/harden-llm/scripts/run-test-tier.mjs";
const root=process.argv[2];
const tasks=["a","b","c"].map(id=>({id,cleanupOwner:"rca",network:"forbidden",tier:"T0",resourceClass:"cpu",command:[process.execPath,root+"/worker.mjs",root,id],workingDirectory:".",dependsOn:[],timeoutMs:3000}));
await runTasks(tasks,{root,resourceClasses:{cpu:{slots:3}},runDirectory:root+"/run",environment:{}});
