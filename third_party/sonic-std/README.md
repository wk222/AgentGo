# sonic-std

A pure-Go stand-in for `github.com/bytedance/sonic`, used ONLY by the 32-bit
Windows build (`go386.mod`, see `scripts/release-win7-386.ps1`).

Why: upstream sonic refuses to compile on 32-bit targets (its `ast` package
contains a deliberate constant-overflow guard). Upstream already falls back to
`encoding/json` on every platform that is not amd64/arm64 and on Go 1.27+, so on
Win7 (legacy Go 1.27) the real library only ever acted as a thin wrapper around
`encoding/json` anyway. This module keeps exactly that fallback behaviour and
drops the part that cannot be built.

Scope: only the API the dependency graph actually uses (`Marshal`, `Unmarshal`,
`MarshalString`, `UnmarshalString`, `Config{...}.Froze()`, `GetFromString` and
the `ast.Node` returned by it). It is not a general replacement. Anything else
in the upstream API is intentionally missing so that new uses fail to compile
instead of silently behaving differently.

The `Config` / `API` shapes and the encoding/json adapter logic are adapted from
upstream `compat.go` (Apache License 2.0, Copyright ByteDance Inc.); the licence
text is in `LICENSE`.
