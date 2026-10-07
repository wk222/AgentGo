"""Summarize rec.jsonl: one sample per endpoint and the ordered SSE event shapes.

    python summarize.py D:\\dev\\go\\tmp\\crushproto-ref\\rec.jsonl [--full N]
"""
import json
import re
import sys

path = sys.argv[1]
full = int(sys.argv[sys.argv.index("--full") + 1]) if "--full" in sys.argv else 700
rows = [json.loads(l) for l in open(path, encoding="utf-8") if l.strip()]
norm = lambda p: re.sub(r"[0-9a-f]{8}-[0-9a-f-]{27}", "{id}", p.split("?")[0])

print("== REST exchanges (in order) ==")
seen = set()
for r in rows:
    if "method" not in r:
        continue
    key = (r["method"], norm(r["path"]))
    first = key not in seen
    seen.add(key)
    print(f"{r['t']:7} {r['method']:6} {norm(r['path'])} -> {r.get('status')}")
    if first:
        if r.get("req"):
            print("        req :", r["req"][:full].replace("\n", " "))
        if r.get("resp"):
            print("        resp:", r["resp"][:full].replace("\n", " "))

print("\n== SSE events (in order; payload truncated) ==")
buf = ""
for r in rows:
    if "sse_of" not in r:
        continue
    buf += r["data"]
    while "\n\n" in buf:
        ev, buf = buf.split("\n\n", 1)
        for line in ev.splitlines():
            if line.startswith("data: "):
                try:
                    j = json.loads(line[6:])
                except Exception:
                    print(f"{r['t']:7} (unparsed) {line[:120]}")
                    continue
                inner = j.get("payload", {})
                sub = inner.get("type") if isinstance(inner, dict) else ""
                body = json.dumps(inner.get("payload", inner) if isinstance(inner, dict) else inner, ensure_ascii=False)
                print(f"{r['t']:7} {j.get('type')}/{sub}  {body[:full]}")
            else:
                print(f"{r['t']:7} (sse-line) {line[:120]}")
