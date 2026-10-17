#!/usr/bin/env python3
"""Report how much vendored Crush code each AgentGo entry point pulls in.

Usage: python scripts/crush-closure.py [--json]

The digestion plan (docs/plan-crush-digestion.md) uses these numbers as its
progress metric: every phase must make some closure smaller. Lines are non-test
Go lines per package directory.
"""
import json
import os
import subprocess
import sys

AG = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PREFIX = 'agentgo/third_party/crush/'


def dir_lines(d):
    try:
        return sum(
            open(os.path.join(d, f), encoding='utf-8', errors='ignore').read().count('\n')
            for f in os.listdir(d)
            if f.endswith('.go') and not f.endswith('_test.go')
        )
    except OSError:
        return 0


def closure(target):
    out = subprocess.run(
        ['go', 'list', '-deps', '-f', '{{.ImportPath}}|{{.Dir}}', target],
        cwd=AG, capture_output=True, text=True, encoding='utf-8')
    if out.returncode != 0:
        sys.exit(f'go list {target} failed:\n{out.stderr.strip()}')
    pkgs = {}
    for line in out.stdout.splitlines():
        if '|' not in line:
            continue
        ip, d = line.split('|', 1)
        if ip.startswith(PREFIX):
            pkgs[ip] = dir_lines(d)
    return pkgs


def disk_total():
    total = pkgs = 0
    for root, _dirs, files in os.walk(os.path.join(AG, 'third_party', 'crush')):
        go = [f for f in files if f.endswith('.go') and not f.endswith('_test.go')]
        if go:
            pkgs += 1
            total += sum(open(os.path.join(root, f), encoding='utf-8', errors='ignore').read().count('\n') for f in go)
    return pkgs, total


def main():
    entries = {
        'codetools (./internal/codetools)': './internal/codetools',
        'tui (./cmd/agentgo-tui)': './cmd/agentgo-tui',
        'all internal (./internal/...)': './internal/...',
    }
    report = {}
    for name, target in entries.items():
        pkgs = closure(target)
        report[name] = {'packages': len(pkgs), 'lines': sum(pkgs.values())}
    # ./internal/... misses cmd/agentgo-tui; union it in for the total.
    pkgs, lines = disk_total()
    report['third_party/crush on disk'] = {'packages': pkgs, 'lines': lines}

    if '--json' in sys.argv:
        print(json.dumps(report, indent=2))
        return
    for name, r in report.items():
        print(f"{name:42s} {r['packages']:4d} pkgs {r['lines']:7,} lines")
    pkgs = closure('./internal/codetools')
    print('\ncodetools closure, largest packages:')
    for ip, lines in sorted(pkgs.items(), key=lambda kv: -kv[1])[:10]:
        print(f'  {lines:6,}  {ip[len(PREFIX):]}')


if __name__ == '__main__':
    main()
