"""Stdio smoke client for agentgo-re (MCP, newline-delimited JSON-RPC).

usage: python scripts/re_smoke.py <tool> key=value ... [--exe bin/agentgo-re.exe] [--home DIR]
  values that parse as JSON (numbers, true/false, [..]) are sent as JSON, others as strings.
example:
  python scripts/re_smoke.py re_triage path=D:/x/Assembly-CSharp.dll
  python scripts/re_smoke.py re_apk_inspect path=D:/x/game.apk
"""
import json
import subprocess
import sys


def parse_val(v: str):
    try:
        return json.loads(v)
    except ValueError:
        return v


def main() -> int:
    argv = sys.argv[1:]
    exe, home = "bin/agentgo-re.exe", None
    if "--exe" in argv:
        i = argv.index("--exe"); exe = argv[i + 1]; del argv[i:i + 2]
    if "--home" in argv:
        i = argv.index("--home"); home = argv[i + 1]; del argv[i:i + 2]
    if not argv:
        print(__doc__); return 2
    tool, args = argv[0], {}
    for kv in argv[1:]:
        k, _, v = kv.partition("=")
        args[k] = parse_val(v)
    cmd = [exe] + (["--home", home] if home else [])
    p = subprocess.Popen(cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, encoding="utf-8")

    def rpc(i, method, params=None):
        p.stdin.write(json.dumps({"jsonrpc": "2.0", "id": i, "method": method, "params": params or {}}) + "\n")
        p.stdin.flush()
        return json.loads(p.stdout.readline())

    rpc(1, "initialize", {"protocolVersion": "2024-11-05", "capabilities": {}, "clientInfo": {"name": "smoke", "version": "0"}})
    p.stdin.write(json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}) + "\n"); p.stdin.flush()
    if tool == "list":
        r = rpc(2, "tools/list")
        for t in r["result"]["tools"]:
            print(t["name"], "-", t["description"][:90])
    else:
        r = rpc(2, "tools/call", {"name": tool, "arguments": args})
        res = r.get("result") or r
        for c in res.get("content", []):
            sys.stdout.reconfigure(encoding="utf-8")
            print(c.get("text", c))
        if res.get("isError"):
            return 1
    p.stdin.close(); p.wait(timeout=5)
    return 0


if __name__ == "__main__":
    sys.exit(main())
