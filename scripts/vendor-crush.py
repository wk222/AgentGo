#!/usr/bin/env python3
"""Refresh third_party/crush from a Crush checkout (see third_party/crush/NOTICE.md).

Usage: python scripts/vendor-crush.py <path-to-crush-checkout>

The package set is the set of directories that already hold Go files under
third_party/crush, so a refresh keeps exactly the same packages. To add a
package, create an empty directory with a placeholder .go file first (or copy it
once by hand), then run this script. Afterwards: go mod tidy && go build ./...
"""
import os
import shutil
import sys

AG = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DST = os.path.join(AG, 'third_party', 'crush')
OLD = 'github.com/charmbracelet/crush'
NEW = 'agentgo/third_party/crush'


def package_dirs(root):
    out = []
    for r, dirs, files in os.walk(root):
        if any(f.endswith('.go') for f in files):
            out.append(os.path.relpath(r, root))
    return sorted(out)


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    src = os.path.abspath(sys.argv[1])
    if not os.path.isfile(os.path.join(src, 'go.mod')):
        sys.exit(f'{src} is not a Go module checkout')
    pkgs = package_dirs(DST)
    if not pkgs:
        sys.exit('third_party/crush has no packages to refresh')
    pkg_set = {os.path.normcase(os.path.join(src, p)) for p in pkgs}

    for rel in pkgs:
        s, d = os.path.join(src, rel), os.path.join(DST, rel)
        if not os.path.isdir(s):
            print(f'skip (gone upstream): {rel}')
            continue
        # replace this package's own files, keep nested packages untouched
        for f in os.listdir(d):
            p = os.path.join(d, f)
            if os.path.isfile(p):
                os.remove(p)
        for root, dirs, files in os.walk(s):
            dirs[:] = [x for x in dirs if x != 'testdata' and os.path.normcase(os.path.join(root, x)) not in pkg_set]
            r = os.path.relpath(root, s)
            tgt = d if r == '.' else os.path.join(d, r)
            os.makedirs(tgt, exist_ok=True)
            for f in files:
                if not f.endswith('_test.go'):
                    shutil.copy2(os.path.join(root, f), os.path.join(tgt, f))

    for name in ('LICENSE.md', 'LICENSE'):
        if os.path.exists(os.path.join(src, name)):
            shutil.copy2(os.path.join(src, name), os.path.join(DST, name))

    n = 0
    for root, _, files in os.walk(DST):
        for f in files:
            if f.endswith('.go'):
                p = os.path.join(root, f)
                t = open(p, encoding='utf-8').read()
                if OLD in t:
                    open(p, 'w', encoding='utf-8', newline='').write(t.replace(OLD, NEW))
                    n += 1
    print(f'refreshed {len(pkgs)} packages, rewrote imports in {n} files')
    print('next: go mod tidy && go build ./... ; update the commit hash in third_party/crush/NOTICE.md')


if __name__ == '__main__':
    main()
