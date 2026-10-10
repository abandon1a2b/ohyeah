#!/usr/bin/env python3
"""Ohyeah HTTP client. Requires Python 3.9+, no third-party dependencies."""
import argparse
import http.client
import json
import math
import os
import queue
import sys
import threading
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
            with urllib.request.urlopen(request, timeout=timeout) as response:
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


def main():
    parser = Parser(description="Retrieve prior work context with provenance", allow_abbrev=False)
    parser.add_argument("command", choices=["projects", "search", "get", "status"])
    parser.add_argument("value", nargs="?")
    parser.add_argument("--server", default=os.environ.get("OHYEAH_SERVER", DEFAULT_SERVER))
    parser.add_argument("--timeout", type=float, default=10)
    parser.add_argument("--json", action="store_true", help="emit JSON (also the default)")
    parser.add_argument("--project")
    parser.add_argument("--type")
    parser.add_argument("--mount")
    parser.add_argument("--workspace")
    parser.add_argument("--kind", help="comma-separated memory kinds")
    parser.add_argument("--mode", choices=["keyword", "hybrid", "semantic"])
    parser.add_argument("--limit", type=int, default=10)
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
    if args.command in ("search", "get"):
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
        params = {"q": args.value, "project": args.project, "limit": args.limit}
        for name in ("type", "mount", "workspace", "kind", "mode"):
            value = getattr(args, name)
            if value is not None:
                params[name] = value
        path = "/search?" + urllib.parse.urlencode(params)
    elif args.command == "get":
        path = "/memories/" + urllib.parse.quote(args.value, safe="")
    else:
        path = "/" + args.command
    request = urllib.request.Request(args.server.rstrip("/") + "/api/v1" + path, headers={"Accept": "application/json"})
    code, result = fetch(request, args.timeout)
    print(json.dumps(result, ensure_ascii=False, indent=2 if code == 0 else None),
          file=sys.stdout if code == 0 else sys.stderr)
    return code


if __name__ == "__main__":
    sys.exit(main())
