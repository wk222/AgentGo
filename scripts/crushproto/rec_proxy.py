"""Recording reverse proxy for the Crush /v1 protocol.

Sits between a Crush client (`crush`, `crush run`) and a real `crush server`
and writes every exchange to a JSONL file, so the AgentGo protocol adapter can
be built against what the client really sends/expects (not against guesses).

    python rec_proxy.py --listen 18766 --upstream 18765 --out rec.jsonl

SSE responses are logged chunk by chunk with timestamps. Request/response
bodies are truncated; headers are NOT recorded (they may carry credentials).
"""
import argparse
import http.client
import json
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

def scrub(text):
    """Drop the process-environment dump the client/server exchange (secrets!)."""
    try:
        j = json.loads(text)
    except Exception:
        return text
    if isinstance(j, dict) and "env" in j:
        j["env"] = ["<scrubbed>"]
        return json.dumps(j, ensure_ascii=False)
    return text


LOCK = threading.Lock()
T0 = time.time()
MAX_BODY = 20000
MAX_SSE_CHUNKS = 600


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--listen", type=int, required=True)
    ap.add_argument("--upstream", type=int, required=True)
    ap.add_argument("--out", required=True)
    args = ap.parse_args()
    out = open(args.out, "w", encoding="utf-8")

    def log(rec):
        with LOCK:
            out.write(json.dumps(rec, ensure_ascii=False) + "\n")
            out.flush()

    class H(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.0"  # body ends at close: simplest faithful SSE passthrough

        def log_message(self, *a):
            pass

        def _handle(self):
            n = int(self.headers.get("Content-Length") or 0)
            body = self.rfile.read(n) if n else b""
            rec = {"t": round(time.time() - T0, 3), "method": self.command, "path": self.path,
                   "req": scrub(body.decode("utf-8", "replace"))[:MAX_BODY]}
            conn = http.client.HTTPConnection("127.0.0.1", args.upstream, timeout=600)
            hdrs = {k: v for k, v in self.headers.items() if k.lower() not in ("host", "connection")}
            try:
                conn.request(self.command, self.path, body=body or None, headers=hdrs)
                resp = conn.getresponse()
            except Exception as e:  # upstream down
                rec["error"] = str(e)
                log(rec)
                self.send_error(502)
                return
            ctype = resp.getheader("Content-Type", "")
            rec["status"] = resp.status
            rec["ctype"] = ctype
            self.send_response(resp.status)
            for k, v in resp.getheaders():
                if k.lower() in ("content-type", "cache-control"):
                    self.send_header(k, v)
            self.end_headers()
            if "event-stream" in ctype:
                log(rec)  # the stream may never end cleanly: record the open first
                n_chunks = 0
                try:
                    while True:
                        c = resp.read1(65536) if hasattr(resp, "read1") else resp.read(4096)
                        if not c:
                            break
                        if n_chunks < MAX_SSE_CHUNKS:
                            n_chunks += 1
                            log({"t": round(time.time() - T0, 3), "sse_of": self.path,
                                 "data": c.decode("utf-8", "replace")[:MAX_BODY]})
                        self.wfile.write(c)
                        self.wfile.flush()
                except (BrokenPipeError, ConnectionResetError):
                    pass
                conn.close()
                return
            else:
                data = resp.read()
                rec["resp"] = scrub(data.decode("utf-8", "replace"))[:MAX_BODY]
                self.wfile.write(data)
            log(rec)
            conn.close()

        do_GET = do_POST = do_PUT = do_DELETE = do_PATCH = _handle

    srv = ThreadingHTTPServer(("127.0.0.1", args.listen), H)
    srv.daemon_threads = True
    print(f"recording :{args.listen} -> :{args.upstream} -> {args.out}", flush=True)
    srv.serve_forever()


if __name__ == "__main__":
    main()
