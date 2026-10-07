"""Print compact request/response schemas for /v1 paths from a Crush openapi.json.

    python oapi.py openapi.json "/workspaces/{id}/agent" "/clients/{client_id}" ...
    python oapi.py openapi.json --all            # every path, one line each
"""
import json
import sys

spec = json.load(open(sys.argv[1], encoding="utf-8"))
S = spec.get("components", {}).get("schemas", {})  # this spec inlines its schemas


def res(s):
    while "$ref" in s:
        s = S[s["$ref"].split("/")[-1]]
    return s


def brief(s, depth=0):
    s = res(s)
    t = s.get("type")
    if t == "object" and depth < 2:
        props = s.get("properties", {})
        return "{" + ", ".join(f"{k}:{brief(v, depth + 1)}" for k, v in props.items()) + "}"
    if t == "array":
        return "[" + brief(s["items"], depth + 1) + "]"
    return t or "?"


want = None if "--all" in sys.argv else set(sys.argv[2:])
for p, v in spec["paths"].items():
    q = p.replace("/v1", "", 1)
    if want is not None and q not in want:
        continue
    for m, op in v.items():
        rq = op.get("requestBody", {}).get("content", {}).get("application/json", {}).get("schema")
        rs = {}
        for code, x in op.get("responses", {}).items():
            c = x.get("content", {}).get("application/json")
            rs[code] = brief(c["schema"]) if c else "-"
        print(m.upper(), q, "| req:", brief(rq) if rq else "-", "| resp:", rs)
