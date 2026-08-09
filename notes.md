# gdu-viewer-go Notes

Last reviewed: 2026-08-09

## Current state

- The Python implementation has been replaced by a Go CLI that reads `gdu` or `ncdu` JSON, normalizes it, and writes a static HTML report.
- Report data is serialized as `gds-binary-v1`, gzip-compressed, base85-encoded, and embedded into the HTML payload.
- Optional password protection uses PBKDF2-SHA256 and ChaCha20-Poly1305. The browser side can decrypt through Web Crypto or the JavaScript fallback in the generated report.
- Browser search now builds a compact node-reference index only after the first query. It retains no lowercase path copies or trigram posting lists and enforces node/character memory budgets.
- Generated reports embed their folder and copy icons, so they remain fully functional offline.

## Review findings

1. Browser code is maintained through brittle exact-string replacements.
   - `replaceReportDataLoader`, `replaceSearchIndexBuild`, `replaceFolderIcon`, and related helpers silently do nothing if the minified/generated HTML changes in a way that breaks the match.
   - Some required markers are checked, but most optimization patches are not verified.
   - Recommended fix: keep the editable HTML/CSS/JS source outside `example/report.html`, generate `template_data.go` from that source, and fail generation when an expected transform is absent.

2. Cryptography implementation is custom.
   - The code has RFC-vector coverage for ChaCha20-Poly1305 and a PBKDF2 test vector, which is good.
   - For long-term maintenance, prefer audited library implementations when dependency policy allows it. If the no-dependency constraint stays, keep the RFC vectors and add browser round-trip coverage for encrypted payloads.

## Optimization notes

- The biggest wins already landed are binary payload serialization, gzip, base85, direct browser binary parse, virtualized rows, a bounded top-file list, directory-only path lookup, parent references instead of a global parent map, and lazy budgeted search.
- Further output-size improvement is most likely to come from reducing duplicated browser source in the generated HTML, minifying only once from source, and subsetting the embedded fonts.
- Further memory improvement requires profiling large reports and, if necessary, streaming decompression or a chunked flat record format. Browser storage is not a portable dependency for standalone `file://` reports.

## Validation commands

```sh
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./...
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go build -o /tmp/gds .
/tmp/gds example/sample-gdu.json -o /tmp/webdiskstat-report.html
node -e 'const fs=require("fs");const html=fs.readFileSync("/tmp/webdiskstat-report.html","utf8");const scripts=[...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].map(m=>m[1]).join("\n");new Function(scripts);console.log("script syntax ok")'
```

`node` is optional and used only for the generated-report syntax check. Run `go generate` after intentionally updating `example/report.html` so `template_data.go` stays in sync.

## Next recommended fixes

1. Split browser source from generated sample output so changes are edited in source files rather than in generated HTML.
2. Add browser-level accessibility and responsive regression tests.
3. Profile treemap and search-index work with million-node exports before increasing browser data limits.
