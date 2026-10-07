"""Send one prompt to an existing crushproto session over HTTP and print the answer.

    python ask.py 127.0.0.1:18770 D:\\proj "<prompt>"   # uses the newest session of the project
"""
import http.client
import json
import sys
import threading
import uuid


def call(host, method, path, body=None):
    c = http.client.HTTPConnection(host, timeout=20)
    c.request(method, "/v1" + path, json.dumps(body) if body is not None else None,
              {"Content-Type": "application/json"})
    r = c.getresponse()
    data = r.read()
    return json.loads(data) if data else None


def main():
    host, path, prompt = sys.argv[1], sys.argv[2], sys.argv[3]
    ws = call(host, "POST", "/workspaces", {"path": path, "data_dir": "", "client_id": str(uuid.uuid4()), "env": []})
    wid = ws["id"]
    sessions = call(host, "GET", f"/workspaces/{wid}/sessions")
    if sessions:
        sid = sorted(sessions, key=lambda s: s.get("updated_at", 0))[-1]["id"]
    else:
        sid = call(host, "POST", f"/workspaces/{wid}/sessions", {"title": "ask"})["id"]

    done = threading.Event()
    answer = {}

    def listen():
        c = http.client.HTTPConnection(host, timeout=300)
        c.request("GET", f"/v1/workspaces/{wid}/events")
        r = c.getresponse()
        for raw in r:
            line = raw.decode("utf-8", "replace").strip()
            if line.startswith("data: ") and '"run_complete"' in line:
                answer.update(json.loads(line[6:])["payload"]["payload"])
                done.set()
                return

    threading.Thread(target=listen, daemon=True).start()
    call(host, "POST", f"/workspaces/{wid}/agent", {"session_id": sid, "run_id": "ask-1", "prompt": prompt})
    if not done.wait(240):
        print("TIMEOUT")
        return
    print("ANSWER:", answer.get("text"))


if __name__ == "__main__":
    sys.stdout.reconfigure(encoding="utf-8")
    main()
