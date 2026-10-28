import { mkdtemp, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { spawn } from "node:child_process";

const root = await mkdtemp(join(tmpdir(), "ohyeah-e2e-"));
const collector = join(root, "collector.mjs");
await writeFile(collector, `process.stdin.resume(); process.stdin.on('end', () => {
 console.log(JSON.stringify({type:'document', document:{externalId:'fixture',modifiedAt:'2026-01-01T00:00:00Z',reference:{url:'https://example.com/evidence'},units:[{key:'result',kind:'conclusion',title:'Regression evidence',content:'configuration lifecycle verified'}]}}));
 console.log(JSON.stringify({type:'complete',mode:'snapshot'}));
});`);
const config = join(root, "config.yaml");
const external = process.env.OHYEAH_E2E_MEILISEARCH;
const children = [];
function launch(executable, args) {
 const child = spawn(executable, args, { stdio: "inherit" });
 children.push(child);
 child.on("error", error => { console.error(error); void stop(1); });
 child.on("exit", () => { if (!stopping) void stop(1); });
 return child;
}
let stopping = false;
async function stop(code = 0) {
 if (stopping) return;
 stopping = true;
 const waits = children.map(child => new Promise(resolve => {
  if (child.exitCode !== null || child.signalCode !== null) return resolve();
  child.once("exit", resolve); child.kill("SIGTERM");
 }));
 const timeout = setTimeout(() => children.forEach(child => child.kill("SIGKILL")), 10000);
 await Promise.all(waits); clearTimeout(timeout);
 await rm(root, { recursive: true, force: true });
 process.exit(code);
}
process.on("SIGTERM", () => void stop());
process.on("SIGINT", () => void stop());
if (external) {
 launch(external, ["--http-addr", "127.0.0.1:18770", "--db-path", join(root, "search"), "--no-analytics"]);
}
await writeFile(config, `state_path: ${JSON.stringify(join(root, "state.db"))}
server:
  address: 127.0.0.1:18787
meilisearch:
  mode: ${external ? "external" : "managed"}
  url: http://127.0.0.1:18770
  directory: ${JSON.stringify(join(root, "managed"))}
projects:
  fixture:
    workspace: ${JSON.stringify(root)}
    types:
      documents:
        collector:
          command: ${JSON.stringify(process.execPath)}
          args: [${JSON.stringify(collector)}]
          revision: 1
        mounts:
          main:
            root: ${JSON.stringify(root)}
            revision: 1
`);
launch(resolve("ohyeah"), ["--config", config, "serve"]);
