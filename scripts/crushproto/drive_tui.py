"""Drive the real Crush TUI in a pseudo-terminal and print what it renders.

    $env:PYTHONPATH = "D:\\dev\\pytools"          # pywinpty + pyte (pip install --target)
    python drive_tui.py --exe crush.exe --host tcp://127.0.0.1:18770 --cwd D:\\ws \\
        --step wait:7 --step "send:make hello\\r" --step wait:6

Steps: wait:<sec> (then dump the screen) | send:<text with \\r \\x03 escapes> | quit
"""
import argparse
import codecs
import os
import sys
import threading
import time

from winpty import PtyProcess
import pyte


def main():
    sys.stdout.reconfigure(encoding="utf-8")
    ap = argparse.ArgumentParser()
    ap.add_argument("--exe", required=True)
    ap.add_argument("--host", default="")
    ap.add_argument("--embedded", action="store_true",
                    help="run exe directly (agentgo-tui): no -H/CRUSH_CLIENT_SERVER; pass flags with --arg")
    ap.add_argument("--arg", action="append", default=[])
    ap.add_argument("--cwd", required=True)
    ap.add_argument("--data-dir", default="")
    ap.add_argument("--rows", type=int, default=45)
    ap.add_argument("--cols", type=int, default=130)
    ap.add_argument("--step", action="append", default=[])
    a = ap.parse_args()

    env = dict(os.environ)
    env["TERM"] = "xterm-256color"
    if a.embedded:
        argv = [a.exe] + a.arg
    else:
        env["CRUSH_CLIENT_SERVER"] = "1"
        argv = [a.exe, "-H", a.host]
        if a.data_dir:
            argv += ["-D", a.data_dir]
    p = PtyProcess.spawn(argv, cwd=a.cwd, env=env, dimensions=(a.rows, a.cols))

    class Screen(pyte.Screen):
        # Bubble Tea emits CSI ? ... m, which pyte forwards with private=True.
        def select_graphic_rendition(self, *attrs, private=False):
            if not private:
                super().select_graphic_rendition(*attrs)

    screen = Screen(a.cols, a.rows)
    stream = pyte.Stream(screen)
    lock = threading.Lock()
    stop = False

    def pump():
        while not stop and p.isalive():
            try:
                data = p.read(4096)
            except EOFError:
                break
            except Exception:
                break
            if data:
                with lock:
                    stream.feed(data)

    t = threading.Thread(target=pump, daemon=True)
    t.start()

    def dump(label):
        with lock:
            lines = [l.rstrip() for l in screen.display]
        while lines and not lines[-1]:
            lines.pop()
        print(f"\n===== {label} (alive={p.isalive()}) =====")
        print("\n".join(lines))
        sys.stdout.flush()

    try:
        for i, s in enumerate(a.step):
            kind, _, arg = s.partition(":")
            if kind == "wait":
                time.sleep(float(arg))
                dump(f"step {i}: after wait {arg}s")
            elif kind == "send":
                p.write(codecs.decode(arg, "unicode_escape"))
            elif kind == "quit":
                break
    finally:
        stop = True
        try:
            p.terminate(force=True)
        except Exception:
            pass


if __name__ == "__main__":
    main()
