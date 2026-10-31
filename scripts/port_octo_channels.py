"""一次性移植脚本：把 octo-agent 的 IM 适配器拷到 agentgo/internal/imchannel。

只拷平台无关的适配器层（adapter/chunking/markdown/config/notify + 6 个适配器 + uploads），
不拷 octo 的 manager（与其 agent 耦合），manager 在 agentgo 里重写。
"""
import os
import re
import shutil
import sys

SRC = r"C:\Users\user\Documents\GitHub\octo-agent-main\internal"
DST = r"C:\Users\user\Documents\GitHub\agentgo\internal\imchannel"

CORE = ["adapter.go", "chunking.go", "chunking_test.go", "markdown.go", "markdown_test.go",
        "config.go", "config_test.go", "notify.go", "notify_test.go"]

REWRITES = [
    ("github.com/open-octo/octo-agent/internal/channel/adapters/", "agentgo/internal/imchannel/adapters/"),
    ("github.com/open-octo/octo-agent/internal/channel", "agentgo/internal/imchannel"),
    ("github.com/open-octo/octo-agent/internal/uploads", "agentgo/internal/imchannel/uploads"),
    ("github.com/open-octo/octo-agent/internal/datahome", "agentgo/internal/imchannel/datahome"),
]


def convert(text: str) -> str:
    for a, b in REWRITES:
        text = text.replace(a, b)
    return text


def copy_file(src: str, dst: str) -> None:
    os.makedirs(os.path.dirname(dst), exist_ok=True)
    with open(src, "r", encoding="utf-8") as f:
        text = f.read()
    text = convert(text)
    with open(dst, "w", encoding="utf-8", newline="\n") as f:
        f.write(text)


def main() -> None:
    if os.path.exists(os.path.join(DST, "adapter.go")):
        print("target exists, refusing to overwrite:", DST)
        sys.exit(1)
    for name in CORE:
        copy_file(os.path.join(SRC, "channel", name), os.path.join(DST, name))
    adapters = os.path.join(SRC, "channel", "adapters")
    for root, _, files in os.walk(adapters):
        for fn in files:
            if not fn.endswith(".go"):
                continue
            rel = os.path.relpath(os.path.join(root, fn), adapters)
            copy_file(os.path.join(root, fn), os.path.join(DST, "adapters", rel))
    for fn in ("uploads.go", "uploads_test.go"):
        copy_file(os.path.join(SRC, "uploads", fn), os.path.join(DST, "uploads", fn))
    print("ported")


if __name__ == "__main__":
    main()
