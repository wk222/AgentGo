"""Capture GET responses of a real `crush server` as fixtures for the AgentGo adapter.

    python capture_fixtures.py --port 18765 --workdir D:\\ws --datadir D:\\data --out fixtures

Creates its own workspace, GETs the endpoints the TUI polls, writes
<out>/<name>.json = {"status": int, "body": <json|text>} and removes the workspace.
The process-environment dump ("env") is never stored.
"""
import argparse
import http.client
import json
import os
import sys
import uuid

ENDPOINTS = {
    "health": "/v1/health",
    "version": "/v1/version",
    "global_config": "/v1/config",
    "workspace": "/v1/workspaces/{id}",
    "ws_config": "/v1/workspaces/{id}/config",
    "providers": "/v1/workspaces/{id}/providers",
    "agent": "/v1/workspaces/{id}/agent",
    "sessions": "/v1/workspaces/{id}/sessions",
    "messages_user": "/v1/workspaces/{id}/messages/user",
    "lsps": "/v1/workspaces/{id}/lsps",
    "mcp_states": "/v1/workspaces/{id}/mcp/states",
    "mcp_pending_auth": "/v1/workspaces/{id}/mcp/pending-auth",
    "mcp_disabled": "/v1/workspaces/{id}/mcp/disabled",
    "mcp_enabled": "/v1/workspaces/{id}/mcp/enabled",
    "mcp_prompts": "/v1/workspaces/{id}/mcp/prompts",
    "skills": "/v1/workspaces/{id}/skills",
    "permissions_skip": "/v1/workspaces/{id}/permissions/skip",
    "needs_init": "/v1/workspaces/{id}/project/needs-init",
    "init_prompt": "/v1/workspaces/{id}/project/init-prompt",
    "git_branch": "/v1/workspaces/{id}/git/branch",
    "default_small_model": "/v1/workspaces/{id}/agent/default-small-model?provider_id=azure",
}


def call(port, method, path, body=None):
    c = http.client.HTTPConnection("127.0.0.1", port, timeout=30)
    data = json.dumps(body) if body is not None else None
    c.request(method, path, body=data, headers={"Content-Type": "application/json"} if data else {})
    r = c.getresponse()
    raw = r.read().decode("utf-8", "replace")
    c.close()
    return r.status, raw


def scrub(obj):
    if isinstance(obj, dict) and "env" in obj:
        obj["env"] = []
    return obj


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, required=True)
    ap.add_argument("--workdir", required=True)
    ap.add_argument("--datadir", required=True)
    ap.add_argument("--out", required=True)
    a = ap.parse_args()
    os.makedirs(a.out, exist_ok=True)

    cid = str(uuid.uuid4())
    st, raw = call(a.port, "POST", "/v1/workspaces", {
        "id": "", "path": a.workdir, "data_dir": a.datadir, "version": "", "client_id": cid, "env": []})
    if st != 200:
        sys.exit(f"create workspace failed: {st} {raw[:300]}")
    wsid = json.loads(raw)["id"]
    try:
        for name, tmpl in ENDPOINTS.items():
            st, raw = call(a.port, "GET", tmpl.replace("{id}", wsid))
            try:
                body = scrub(json.loads(raw))
            except Exception:
                body = raw
            with open(os.path.join(a.out, name + ".json"), "w", encoding="utf-8") as f:
                json.dump({"status": st, "body": body}, f, ensure_ascii=False, indent=1)
            print(f"{st} {name}  ({len(raw)} bytes)")
    finally:
        call(a.port, "DELETE", "/v1/workspaces/" + wsid)
        call(a.port, "DELETE", "/v1/clients/" + cid)


if __name__ == "__main__":
    main()
