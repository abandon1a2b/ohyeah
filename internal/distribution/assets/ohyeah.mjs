#!/usr/bin/env node
// Requires Node.js 18+, no third-party dependencies.
const DEFAULT_SERVER = __SERVER_JSON__;
const help = `Usage: node ohyeah.mjs [--server URL] <command> [options]

Commands:
  projects
  search <query> --project <id> [--type ID] [--mount ID] [--workspace ID]
         [--kind KIND,...] [--mode keyword|hybrid|semantic] [--limit N]
  get <memory-id>
  status

Options: --server URL, --timeout SECONDS (default 10), --json, -h/--help
Output is always JSON. OHYEAH_SERVER overrides the packaged default URL.
Exit codes: 0 success, 1 HTTP/transport error, 2 invalid arguments.`;

function invalid(message) {
  process.stderr.write(JSON.stringify({ error: message }) + "\n");
  process.exit(2);
}

async function main() {
  const options = { server: process.env.OHYEAH_SERVER ?? DEFAULT_SERVER, timeout: "10", limit: "10" };
  const names = new Set(["server", "timeout", "project", "type", "mount", "workspace", "kind", "mode", "limit"]);
  const positional = [];
  let literal = false;
  for (let i = 2; i < process.argv.length; i++) {
    const arg = process.argv[i];
    if (!literal && (arg === "--help" || arg === "-h")) { console.log(help); return; }
    if (!literal && arg === "--") { literal = true; continue; }
    if (!literal && arg === "--json") continue;
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
  if (!["projects", "search", "get", "status"].includes(command)) invalid("command must be projects, search, get or status");
  if (positional.length !== (["search", "get"].includes(command) ? 2 : 1)) invalid("invalid number of arguments for " + command);
  if (["search", "get"].includes(command) && !value.trim()) invalid(command + " requires a nonempty argument");
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
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), seconds * 1000);
  try {
    const response = await fetch(options.server.replace(/\/+$/, "") + "/api/v1" + path, { headers: { Accept: "application/json" }, signal: controller.signal });
    let result;
    try { result = await response.json(); }
    catch {
      if (controller.signal.aborted) throw new Error("request timed out");
      if (response.ok) throw new Error("server returned invalid JSON");
      result = { error: "HTTP " + response.status };
    }
    if (!response.ok) {
      process.stderr.write(JSON.stringify({ error: String(result?.error ?? "HTTP " + response.status), status: response.status }) + "\n");
      process.exitCode = 1;
      return;
    }
    console.log(JSON.stringify(result, null, 2));
  } catch (error) {
    process.stderr.write(JSON.stringify({ error: controller.signal.aborted ? "request timed out" : error.message }) + "\n");
    process.exitCode = 1;
  } finally { clearTimeout(timer); }
}

await main();
