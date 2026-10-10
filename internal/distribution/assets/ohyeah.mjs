#!/usr/bin/env node
// Requires Node.js 18+, no third-party dependencies.
import { readFile, mkdir, writeFile, rename, unlink } from 'node:fs/promises';
import { createHash, randomBytes, randomUUID } from 'node:crypto';
import { homedir } from 'node:os';
import { join, dirname, isAbsolute } from 'node:path';
import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';
const DEFAULT_SERVER = __SERVER_JSON__;
const help = `Usage: node ohyeah.mjs [--server URL] <command> [options]

Commands:
  projects
  search <query> --project <id> [--type ID] [--mount ID] [--workspace ID]
         [--kind KIND,...] [--mode keyword|hybrid|semantic] [--limit N]
  get <memory-id>
  status
  login [--no-browser]
  logout
  whoami
  tokens
  revoke <credential-id>
  sync <source-id>
  upload-state <source-id>
  prepare-upload <source-id> --file collector.ndjson
  prepare-upload <source-id> --collector EXEC [--collector-args JSON] [--root PATH] [--collector-timeout SECONDS]
  upload <source-id> --file prepared-batch.json

Options: --server URL, --timeout SECONDS (default 10), --json, -h/--help
Output is always JSON. OHYEAH_SERVER overrides the packaged default URL.
Exit codes: 0 success, 1 HTTP/transport error, 2 invalid arguments.`;

function invalid(message) {
  process.stderr.write(JSON.stringify({ error: message }) + "\n");
  process.exit(2);
}

async function main() {
  const options = { server: process.env.OHYEAH_SERVER ?? DEFAULT_SERVER, timeout: "10", limit: "10" };
  const names = new Set(["server", "timeout", "project", "type", "mount", "workspace", "kind", "mode", "limit", "file", "collector", "collector-args", "collector-timeout", "root"]);
  const positional = [];
  let literal = false;
  for (let i = 2; i < process.argv.length; i++) {
    const arg = process.argv[i];
    if (!literal && (arg === "--help" || arg === "-h")) { console.log(help); return; }
    if (!literal && arg === "--") { literal = true; continue; }
    if (!literal && arg === "--json") continue;
    if (!literal && arg === "--no-browser") { options.noBrowser = true; continue; }
    if (!literal && arg.startsWith("--")) {
      const equal = arg.indexOf("=");
      const name = arg.slice(2, equal < 0 ? undefined : equal);
      if (!names.has(name)) invalid("unknown option: --" + name);
      const value = equal < 0 ? process.argv[++i] : arg.slice(equal + 1);
      if (value === undefined || (equal < 0 && value.startsWith("--"))) invalid("--" + name + " requires a value");
      options[name] = value;
    } else if (!literal && arg.startsWith("-")) invalid("unknown option: " + arg);
    else positional.push(arg);
  }
  const [command, value] = positional;
  const withValue=['search','get','revoke','sync','upload-state','prepare-upload','upload'];
  if (!["projects", "search", "get", "status", "login", "logout", "whoami", "tokens", "revoke", "sync", "upload-state", "prepare-upload", "upload"].includes(command)) invalid("unknown command");
  if (positional.length !== (withValue.includes(command) ? 2 : 1)) invalid("invalid number of arguments for " + command);
  if (withValue.includes(command) && !value.trim()) invalid(command + " requires a nonempty argument");
  if(command==='prepare-upload' && Boolean(options.file)===Boolean(options.collector)) invalid('prepare-upload requires exactly one of --file or --collector');
  if(command==='upload' && !options.file) invalid('upload requires --file with a prepared JSON batch');
  if(options.file && !['prepare-upload','upload'].includes(command)) invalid('--file is only valid for prepare-upload or upload');
  if(command!=='prepare-upload' && ['collector','collector-args','collector-timeout','root'].some(k=>options[k]!==undefined)) invalid('collector options are only valid for prepare-upload');
  let collectorArgs;try{collectorArgs=JSON.parse(options['collector-args']??'[]')}catch{invalid('--collector-args must be a JSON string array')}
  if(!Array.isArray(collectorArgs)||collectorArgs.some(v=>typeof v!=='string'))invalid('--collector-args must be a JSON string array');
  const collectorTimeout=Number(options['collector-timeout']??300);
  if(!Number.isFinite(collectorTimeout)||collectorTimeout<=0||collectorTimeout>3600)invalid('--collector-timeout must be within 1 hour and positive');
  if(options.root&&!isAbsolute(options.root))invalid('--root must be absolute');
  if (options.noBrowser && command !== 'login') invalid('--no-browser is only valid for login');
  const seconds = Number(options.timeout);
  if (!Number.isFinite(seconds) || seconds <= 0 || seconds * 1000 > 2147483647) invalid("--timeout must be a positive number of seconds (at most 2147483)");
  let server;
  try { server = new URL(options.server); } catch { invalid("--server must be an HTTP(S) URL without credentials, query or fragment"); }
  if (!["http:", "https:"].includes(server.protocol) || server.username || server.password || server.search || server.hash) invalid("--server must be an HTTP(S) URL without credentials, query or fragment");
  if (command !== "search" && (["project", "type", "mount", "workspace", "kind", "mode"].some(name => options[name] !== undefined) || options.limit !== "10")) invalid("search options are only valid for search");
  let path;
  if (command === "search") {
    if (!options.project?.trim()) invalid("search requires --project");
    if (!/^\d+$/.test(options.limit) || Number(options.limit) <= 0 || Number(options.limit) > 2147483647) invalid("--limit must be an integer between 1 and 2147483647");
    if (options.mode !== undefined && !["keyword", "hybrid", "semantic"].includes(options.mode)) invalid("mode must be keyword, hybrid or semantic");
    const params = new URLSearchParams({ q: value, project: options.project, limit: options.limit });
    for (const name of ["type", "mount", "workspace", "kind", "mode"]) if (options[name] !== undefined) params.set(name, options[name]);
    path = "/search?" + params;
  } else if (command === "get") path = "/memories/" + encodeURIComponent(value);
  else path = "/" + command;
  const base = options.server.replace(/\/+$/, '');
  const credentialPath = join(process.env.OHYEAH_CREDENTIALS_DIR ?? join(homedir(), '.config', 'ohyeah', 'credentials'), createHash('sha256').update(base).digest('hex') + '.json');
  try {
    let token = process.env.OHYEAH_TOKEN ?? '';
    if (!token) {
      try { token = JSON.parse(await readFile(credentialPath, 'utf8')).token ?? ''; }
      catch (error) { if (error.code !== 'ENOENT') throw error; }
    }
    if ((token || command === 'login') && server.protocol !== 'https:' && !(/^127\.(\d{1,3}\.){2}\d{1,3}$/.test(server.hostname) || server.hostname === '[::1]')) invalid('authenticated requests require HTTPS (HTTP allowed for loopback IPs)');
    const request = async (endpoint, method = 'GET', data, authenticated = true) => {
      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(), seconds * 1000);
      try {
        const headers = { Accept: 'application/json' };
        if (authenticated && token) headers.Authorization = 'Bearer ' + token;
        if (data !== undefined) headers['Content-Type'] = 'application/json';
        const response = await fetch(base + endpoint, { method, headers, body: data === undefined ? undefined : JSON.stringify(data), signal: controller.signal, redirect: 'error' });
        let result;
        try { result = await response.json(); }
        catch {
          if (controller.signal.aborted) throw new Error('request timed out');
          if (response.ok) throw new Error('server returned invalid JSON');
          result = { error: 'HTTP ' + response.status };
        }
        if (!response.ok) { const error = new Error(String(result?.error ?? 'HTTP ' + response.status)); error.status = response.status; throw error; }
        return result;
      } catch (error) {
        if (controller.signal.aborted) throw new Error('request timed out');
        throw error;
      } finally { clearTimeout(timer); }
    };
    let result;
    if (command === 'login') {
      const info = await request('/auth/info', 'GET', undefined, false);
      if (!info.enabled) result = info;
      else {
        const pair = await request('/auth/cli/start', 'POST', {}, false);
        if (new URL(pair.verificationURI).origin !== server.origin) throw new Error('authorization URL does not match service origin');
        process.stderr.write('Authorize CLI at ' + pair.verificationURI + '\nCode: ' + pair.userCode + '\n');
        if (!options.noBrowser) {
          const executable = process.platform === 'darwin' ? 'open' : process.platform === 'win32' ? 'rundll32' : 'xdg-open';
          const args = process.platform === 'win32' ? ['url.dll,FileProtocolHandler', pair.verificationURI] : [pair.verificationURI];
          const child = spawn(executable, args, { detached: true, stdio: 'ignore' });
          child.on('error', () => process.stderr.write('Open the authorization URL manually.\n'));
          child.unref();
        }
        const deadline = Date.now() + Math.min(pair.expiresIn ?? 600, 600) * 1000;
        while (Date.now() < deadline) {
          const exchange = await request('/auth/cli/token', 'POST', { deviceCode: pair.deviceCode, userCode: pair.userCode }, false);
          if (!exchange.pending) {
            await mkdir(dirname(credentialPath), { recursive: true, mode: 0o700 });
            const temporary = credentialPath + '.' + randomBytes(8).toString('hex');
            try {
              await writeFile(temporary, JSON.stringify({ token: exchange.token, expiresAt: exchange.credential.expiresAt }), { mode: 0o600, flag: 'wx' });
              await rename(temporary, credentialPath);
            } finally { await unlink(temporary).catch(error => { if (error.code !== 'ENOENT') throw error; }); }
            result = { loggedIn: true, credential: exchange.credential };
            break;
          }
          await new Promise(resolve => setTimeout(resolve, Math.max(1, Math.min(pair.interval ?? 5, 10)) * 1000));
        }
        if (!result) throw new Error('login expired');
      }
    } else if (command === 'logout') {
      if (token) { try { await request('/auth/logout', 'POST', {}); } catch (error) { if (error.status !== 401) throw error; } }
      await unlink(credentialPath).catch(error => { if (error.code !== 'ENOENT') throw error; });
      result = { loggedOut: true };
    } else if (command === 'whoami') result = await request('/auth/me');
    else if (command === 'tokens') result = await request('/auth/tokens');
    else if (command === 'revoke') result = await request('/auth/tokens/' + encodeURIComponent(value), 'DELETE');
    else if (command === 'sync') result = await request('/api/v1/sync', 'POST', { sourceId: value });
    else if(command==='upload-state'||command==='prepare-upload') {
      const state=await request('/api/v1/uploads/state?'+new URLSearchParams({source:value}));
      if(command==='upload-state')result=state;
      else {
        let text;
        if(options.collector){
          const root=options.root??state.request.mount.root;
          state.request.mount.root=root;if(options.root)state.request.project.workspace=root;
          const promise=promisify(execFile)(options.collector,collectorArgs,{cwd:root,timeout:collectorTimeout*1000,maxBuffer:24*1024*1024,env:{...process.env,OHYEAH_COLLECTOR_PROTOCOL:'1'}});
          promise.child.stdin?.end(JSON.stringify(state.request));
          try { text=(await promise).stdout; } catch { throw new Error('local collector failed or timed out; no batch prepared'); }
        }else text=await readFile(options.file,'utf8');
        if(Buffer.byteLength(text)>24*1024*1024)throw new Error('collector output exceeds 24 MiB; split into delta batches');
        const envelopes=text.split(/\r?\n/).filter(line=>line.trim()).map(line=>JSON.parse(line));
        result={protocolVersion:1,sourceId:value,batchId:randomUUID(),expectedVersion:state.version,scopeRevision:state.scopeRevision,envelopes};
      }
    } else if(command==='upload') {
      const batch=JSON.parse(await readFile(options.file,'utf8'));
      if(batch.sourceId!==value)invalid('batch sourceId does not match command source');
      result=await request('/api/v1/uploads','POST',batch);
    } else result = await request('/api/v1' + path);
    console.log(JSON.stringify(result, null, 2));
  } catch (error) {
    process.stderr.write(JSON.stringify({ error: error.message, ...(error.status ? { status: error.status } : {}) }) + "\n");
    process.exitCode = 1;
  }
}

await main();
