"""Fake OpenAI-compatible chat server that streams reasoning_content then content.

Lets the reasoning path (Eino -> engine -> crushTurn -> Crush TUI) be exercised
without a real reasoning model.

    python fake_llm.py 18999            # serves http://127.0.0.1:18999/v1
    python fake_llm.py 18999 think      # emit <think>..</think> inside content instead

Point AgentGo at it: {"llm": {"api_base": "http://127.0.0.1:18999/v1", "api_key": "x", "model": "fake-r1"}}
"""
import json
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 18999
MODE = sys.argv[2] if len(sys.argv) > 2 else "reasoning_content"

THOUGHTS = ["The user greets me. ", "I should answer briefly ", "and mention the secret word."]
ANSWER = ["Hello! ", "The secret word ", "is PINEAPPLE."]


def chunk(delta, finish=None):
    return {
        "id": "chatcmpl-fake", "object": "chat.completion.chunk", "created": int(time.time()),
        "model": "fake-r1", "choices": [{"index": 0, "delta": delta, "finish_reason": finish}],
    }


class H(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *a):
        sys.stderr.write("[fake_llm] " + fmt % a + "\n")

    def _json(self, code, obj):
        b = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def do_POST(self):
        n = int(self.headers.get("Content-Length") or 0)
        body = json.loads(self.rfile.read(n) or b"{}")
        if not self.path.endswith("/chat/completions"):
            return self._json(404, {"error": {"message": "not implemented: " + self.path}})

        events = [chunk({"role": "assistant", "content": ""})]
        if MODE == "think":
            events.append(chunk({"content": "<think>"}))
            events += [chunk({"content": t}) for t in THOUGHTS]
            events.append(chunk({"content": "</think>"}))
        else:
            events += [chunk({"reasoning_content": t}) for t in THOUGHTS]
        events += [chunk({"content": a}) for a in ANSWER]
        events.append(chunk({}, "stop"))

        if not body.get("stream"):
            text = "".join(ANSWER)
            return self._json(200, {
                "id": "chatcmpl-fake", "object": "chat.completion", "created": int(time.time()),
                "model": "fake-r1",
                "choices": [{"index": 0, "finish_reason": "stop",
                             "message": {"role": "assistant", "content": text,
                                         "reasoning_content": "".join(THOUGHTS)}}],
                "usage": {"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20},
            })

        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()

        def write(data: bytes):
            self.wfile.write(f"{len(data):x}\r\n".encode() + data + b"\r\n")
            self.wfile.flush()

        for ev in events:
            write(f"data: {json.dumps(ev)}\n\n".encode())
            time.sleep(0.35)  # slow enough to watch the TUI's "thinking" state
        write(b"data: [DONE]\n\n")
        self.wfile.write(b"0\r\n\r\n")
        self.wfile.flush()


if __name__ == "__main__":
    ThreadingHTTPServer(("127.0.0.1", PORT), H).serve_forever()
