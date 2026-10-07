"""Print session token usage, tool-call pairing and file history of a crushproto server.

    python query_state.py 127.0.0.1:18770              # inspect live workspaces
    python query_state.py 127.0.0.1:18770 D:\\proj      # register D:\\proj first (restored sessions show up)
"""
import json
import sys
import urllib.request
import uuid


def call(base, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(base + path, data=data, method="POST" if body is not None else "GET",
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=15) as r:
        return json.load(r)


def main():
    base = "http://" + sys.argv[1] + "/v1"
    if len(sys.argv) > 2:
        call(base, "/workspaces", {"path": sys.argv[2], "data_dir": "", "client_id": str(uuid.uuid4()), "env": []})
    for ws in call(base, "/workspaces"):
        wid = ws["id"]
        for s in call(base, f"/workspaces/{wid}/sessions"):
            print(f"session {s['id'][:8]} title={s.get('title')!r} msgs={s.get('message_count')} "
                  f"prompt_tokens={s.get('prompt_tokens')} completion_tokens={s.get('completion_tokens')}")
            for m in call(base, f"/workspaces/{wid}/sessions/{s['id']}/messages"):
                for p in m.get("parts", []):
                    d = p.get("data", {})
                    if p.get("type") in ("tool_call", "tool_result"):
                        print(f"  {m['role']:9} {p['type']:11} id={d.get('id') or d.get('tool_call_id')!r} "
                              f"name={d.get('name')!r} finished={d.get('finished')}")
                    elif p.get("type") == "reasoning":
                        dur = (d.get("finished_at") or 0) - (d.get("started_at") or 0)
                        print(f"  {m['role']:9} {p['type']:11} dur={dur}s thinking={d.get('thinking')[:60]!r}...")
            hist = call(base, f"/workspaces/{wid}/sessions/{s['id']}/history")
            print(f"  history versions: {len(hist)}")
            for f in hist:
                print(f"    v{f['version']} {f['path']} len={len(f['content'])}")


if __name__ == "__main__":
    main()
