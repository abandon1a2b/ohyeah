#!/usr/bin/env python3
"""Ohyeah HTTP client. Requires Python 3.9+, no third-party dependencies."""
import argparse
import http.client
import hashlib
import json
import math
import os
import queue
import sys
import threading
import tempfile
import time
import pathlib
import webbrowser
import uuid
import subprocess
import urllib.error
import urllib.parse
import urllib.request

DEFAULT_SERVER = __SERVER_JSON__


class Parser(argparse.ArgumentParser):
    def error(self, message):
        self.exit(2, json.dumps({"error": message}, ensure_ascii=False) + "\n")


def fetch(request, timeout):
    result = queue.Queue(maxsize=1)

    def run():
        try:
            opener = urllib.request.build_opener(NoRedirect)
            with opener.open(request, timeout=timeout) as response:
                payload = json.load(response)
            result.put((0, payload))
        except urllib.error.HTTPError as error:
            with error:
                try:
                    message = json.load(error).get("error", "HTTP " + str(error.code))
                except (ValueError, AttributeError, OSError, http.client.HTTPException):
                    message = "HTTP " + str(error.code)
            result.put((1, {"error": str(message), "status": error.code}))
        except (urllib.error.URLError, TimeoutError, OSError, ValueError, http.client.HTTPException) as error:
            result.put((1, {"error": str(error)}))

    # A daemon worker bounds the entire request, including slowly streamed bodies.
    threading.Thread(target=run, daemon=True).start()
    try:
        return result.get(timeout=timeout)
    except queue.Empty:
        return 1, {"error": "request timed out"}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def credentials_path(server):
    root = pathlib.Path(os.environ.get("OHYEAH_CREDENTIALS_DIR", pathlib.Path.home() / ".config" / "ohyeah" / "credentials"))
    return root / (hashlib.sha256(server.encode()).hexdigest() + ".json")


def save_credentials(path, data):
    path.parent.mkdir(parents=True, mode=0o700, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(dir=path.parent, prefix=".credential-")
    try:
        with os.fdopen(descriptor, "w") as output:
            json.dump(data, output)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def execute(args, server):
    path = credentials_path(server)
    token = os.environ.get("OHYEAH_TOKEN", "")
    if not token and path.exists():
        token = json.loads(path.read_text()).get("token", "")
    parsed = urllib.parse.urlsplit(server)
    if token or args.command == "login":
        import ipaddress
        try:
            loopback = ipaddress.ip_address(parsed.hostname).is_loopback
        except ValueError:
            loopback = False
        if parsed.scheme != "https" and not loopback:
            return 2, {"error": "authenticated requests require HTTPS (HTTP allowed for loopback IPs)"}

    def request(endpoint, method="GET", data=None, authenticated=True):
        headers = {"Accept": "application/json"}
        if authenticated and token:
            headers["Authorization"] = "Bearer " + token
        if data is not None:
            headers["Content-Type"] = "application/json"
        req = urllib.request.Request(server + endpoint, method=method, headers=headers,
                                     data=json.dumps(data, ensure_ascii=False).encode() if data is not None else None)
        return fetch(req, args.timeout)

    if args.command == "login":
        code, info = request("/auth/info", authenticated=False)
        if code or not info.get("enabled"):
            return code, info
        code, pair = request("/auth/cli/start", "POST", {}, False)
        if code:
            return code, pair
        verification = urllib.parse.urlsplit(pair["verificationURI"])
        if (verification.scheme, verification.netloc) != (parsed.scheme, parsed.netloc):
            return 1, {"error": "authorization URL does not match service origin"}
        print("Authorize CLI at " + pair["verificationURI"] + "\nCode: " + pair["userCode"], file=sys.stderr)
        if not args.no_browser:
            webbrowser.open(pair["verificationURI"])
        deadline = time.monotonic() + min(pair.get("expiresIn", 600), 600)
        while time.monotonic() < deadline:
            code, result = request("/auth/cli/token", "POST", {"deviceCode": pair["deviceCode"], "userCode": pair["userCode"]}, False)
            if code:
                return code, result
            if not result.get("pending"):
                save_credentials(path, {"token": result["token"], "expiresAt": result["credential"]["expiresAt"]})
                return 0, {"loggedIn": True, "credential": result["credential"]}
            time.sleep(max(1, min(pair.get("interval", 5), 10)))
        return 1, {"error": "login expired"}
    if args.command == "logout":
        if token:
            code, result = request("/auth/logout", "POST", {})
            if code and result.get("status") != 401:
                return code, result
        if path.exists():
            path.unlink()
        return 0, {"loggedOut": True}
    if args.command == "whoami":
        return request("/auth/me")
    if args.command == "tokens":
        return request("/auth/tokens")
    if args.command == "revoke":
        return request("/auth/tokens/" + urllib.parse.quote(args.value, safe=""), "DELETE")
    if args.command == "sync":
        return request("/api/v1/sync", "POST", {"sourceId": args.value})
    if args.command in ("upload-state", "prepare-upload"):
        code, state = request("/api/v1/uploads/state?" + urllib.parse.urlencode({"source": args.value}))
        if code or args.command == "upload-state":
            return code, state
        if args.collector:
            collector_request = state["request"]
            root = args.root or collector_request["mount"]["root"]
            collector_request["mount"]["root"] = root
            if args.root:
                collector_request["project"]["workspace"] = root
            output = subprocess.run([args.collector] + json.loads(args.collector_args), input=json.dumps(collector_request),
                                    text=True, capture_output=True, cwd=root, timeout=args.collector_timeout,
                                    env={**os.environ, "OHYEAH_COLLECTOR_PROTOCOL": "1"})
            if output.returncode:
                return 1, {"error": "local collector failed; no batch prepared"}
            text = output.stdout
        else:
            text = pathlib.Path(args.file).read_text()
        if len(text.encode()) > 24 * 1024 * 1024:
            return 1, {"error": "collector output exceeds 24 MiB; split into delta batches"}
        envelopes = [json.loads(line) for line in text.splitlines() if line.strip()]
        return 0, {"protocolVersion": 1, "sourceId": args.value, "batchId": str(uuid.uuid4()),
                   "expectedVersion": state["version"], "scopeRevision": state["scopeRevision"], "envelopes": envelopes}
    if args.command == "upload":
        batch = json.loads(pathlib.Path(args.file).read_text())
        if batch.get("sourceId") != args.value:
            return 2, {"error": "batch sourceId does not match command source"}
        return request("/api/v1/uploads", "POST", batch)
    if args.command == "search":
        params = {"q": args.value, "project": args.project, "limit": args.limit}
        for name in ("type", "mount", "workspace", "kind", "mode"):
            value = getattr(args, name)
            if value is not None:
                params[name] = value
        endpoint = "/search?" + urllib.parse.urlencode(params)
    elif args.command == "get":
        endpoint = "/memories/" + urllib.parse.quote(args.value, safe="")
    else:
        endpoint = "/" + args.command
    return request("/api/v1" + endpoint)


def main():
    parser = Parser(description="Retrieve prior work context with provenance", allow_abbrev=False)
    parser.add_argument("command", choices=["projects", "search", "get", "status", "login", "logout", "whoami", "tokens", "revoke", "sync", "upload-state", "prepare-upload", "upload"])
    parser.add_argument("value", nargs="?")
    parser.add_argument("--server", default=os.environ.get("OHYEAH_SERVER", DEFAULT_SERVER))
    parser.add_argument("--timeout", type=float, default=10)
    parser.add_argument("--json", action="store_true", help="emit JSON (also the default)")
    parser.add_argument("--no-browser", action="store_true", help="print login URL without opening a browser")
    parser.add_argument("--project")
    parser.add_argument("--type")
    parser.add_argument("--mount")
    parser.add_argument("--workspace")
    parser.add_argument("--kind", help="comma-separated memory kinds")
    parser.add_argument("--mode", choices=["keyword", "hybrid", "semantic"])
    parser.add_argument("--limit", type=int, default=10)
    parser.add_argument("--file", help="NDJSON collector output for prepare-upload, prepared JSON batch for upload")
    parser.add_argument("--collector", help="trusted local executable for prepare-upload; executed without a shell")
    parser.add_argument("--collector-args", default="[]", help="JSON string array of local collector arguments")
    parser.add_argument("--collector-timeout", type=float, default=300)
    parser.add_argument("--root", help="absolute local collector working directory")
    args = parser.parse_intermixed_args()
    if not math.isfinite(args.timeout) or args.timeout <= 0 or args.timeout * 1000 > 2147483647:
        parser.error("--timeout must be a positive number of seconds (at most 2147483)")
    try:
        server = urllib.parse.urlsplit(args.server)
        server.port
    except ValueError:
        parser.error("--server must be an HTTP(S) URL without credentials, query or fragment")
    if server.scheme not in ("http", "https") or not server.hostname or server.username or server.password or server.query or server.fragment:
        parser.error("--server must be an HTTP(S) URL without credentials, query or fragment")
    if args.no_browser and args.command != "login":
        parser.error("--no-browser is only valid for login")
    if args.command in ("search", "get", "revoke", "sync", "upload-state", "prepare-upload", "upload"):
        if not args.value or not args.value.strip():
            parser.error(args.command + " requires a nonempty argument")
    elif args.value is not None:
        parser.error(args.command + " does not accept an argument")
    filters = [args.project, args.type, args.mount, args.workspace, args.kind, args.mode]
    if args.command != "search" and (any(v is not None for v in filters) or args.limit != 10):
        parser.error("search options are only valid for search")
    if args.command == "search":
        if not args.project or not args.project.strip():
            parser.error("search requires --project")
        if args.limit <= 0 or args.limit > 2147483647:
            parser.error("--limit must be an integer between 1 and 2147483647")
    if args.command == "prepare-upload" and bool(args.file) == bool(args.collector):
        parser.error("prepare-upload requires exactly one of --file or --collector")
    if args.command == "upload" and not args.file:
        parser.error("upload requires --file with a prepared JSON batch")
    if args.command not in ("prepare-upload", "upload") and args.file:
        parser.error("--file is only valid for prepare-upload or upload")
    if args.command != "prepare-upload" and (args.collector or args.root or args.collector_args != "[]" or args.collector_timeout != 300):
        parser.error("collector options are only valid for prepare-upload")
    try:
        collector_args = json.loads(args.collector_args)
        if not isinstance(collector_args, list) or not all(isinstance(v, str) for v in collector_args):
            parser.error("--collector-args must be a JSON array of strings")
    except ValueError:
        parser.error("--collector-args must be a JSON array of strings")
    if not math.isfinite(args.collector_timeout) or not 0 < args.collector_timeout <= 3600:
        parser.error("--collector-timeout must be within 1 hour and positive")
    if args.root and not pathlib.Path(args.root).is_absolute():
        parser.error("--root must be absolute")
    try:
        code, result = execute(args, args.server.rstrip("/"))
    except subprocess.TimeoutExpired:
        code, result = 1, {"error": "local collector timed out; no batch prepared"}
    except (OSError, ValueError, KeyError) as error:
        code, result = 1, {"error": str(error)}
    print(json.dumps(result, ensure_ascii=False, indent=2 if code == 0 else None),
          file=sys.stdout if code == 0 else sys.stderr)
    return code


if __name__ == "__main__":
    sys.exit(main())
