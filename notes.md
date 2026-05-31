# gdu-viewer-go Notes

Last reviewed: 2026-05-31

## Current state

- The Python implementation has been replaced by a Go CLI that reads `gdu` or `ncdu` JSON, normalizes it, and writes a static HTML report.
- Report data is serialized as `gds-binary-v1`, gzip-compressed, base85-encoded, and embedded into the HTML payload.
- Optional password protection uses PBKDF2-SHA256 and ChaCha20-Poly1305. The browser side can decrypt through Web Crypto or the JavaScript fallback in the generated report.
- Browser startup now loads and renders before building the trigram search candidate index. The index is built in idle chunks after page load.
- The generated folder row icon currently depends on the remote Icons8 PNG URL requested for the folder swatch.

## Review findings

1. `report.go` template transforms are not fully idempotent.
   - `replaceSearchIndexBuild` inserts `scheduleSearchCandidateIndexBuild();` after `renderSafely(); hideLoadingOverlay();` every time it runs against a template that already contains that block.
   - Current generated output has three consecutive calls in `initReport`. The guard inside `scheduleSearchCandidateIndexBuild` prevents duplicate indexing work, but repeated regeneration can keep accumulating dead calls and template noise.
   - Recommended fix: make the insertion conditional on the exact call already being present after `hideLoadingOverlay`, or move the browser source to a first-class template/source file instead of repeatedly patching generated HTML with string replacements.

2. The remote folder icon weakens the "self-contained report" contract.
   - Reports now fetch `https://img.icons8.com/?size=100&id=Vps0Nsl80v4P&format=png&color=000000` at view time.
   - This means offline reports lose the custom folder icon, and opening a report can disclose a request to Icons8.
   - Recommended fix: download and embed the icon as a data URI if the report must remain fully self-contained.

3. Browser code is maintained through brittle exact-string replacements.
   - `replaceReportDataLoader`, `replaceSearchIndexBuild`, `replaceFolderIcon`, and related helpers silently do nothing if the minified/generated HTML changes in a way that breaks the match.
   - Some required markers are checked, but most optimization patches are not verified.
   - Recommended fix: keep the editable HTML/CSS/JS source outside `example/report.html`, generate `template_data.go` from that source, and fail generation when an expected transform is absent.

4. Cryptography implementation is custom.
   - The code has RFC-vector coverage for ChaCha20-Poly1305 and a PBKDF2 test vector, which is good.
   - For long-term maintenance, prefer audited library implementations when dependency policy allows it. If the no-dependency constraint stays, keep the RFC vectors and add browser round-trip coverage for encrypted payloads.

## Optimization notes

- The biggest wins already landed are binary payload serialization, gzip, base85, direct browser binary parse, virtualized rows, cached top-file list, and idle-chunked trigram indexing.
- Further output-size improvement is most likely to come from reducing duplicated browser source in the generated HTML, minifying only once from source, and embedding any external assets as compact data URIs.
- Further render-time improvement should focus on measuring large reports in a browser profile. The likely hotspots are treemap layout, search index population, and DOM row recycling.

## Validation commands

```sh
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./...
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go build -o /tmp/gds .
/home/rwahyudi/bin/gds example/sample-gdu.json -o example/report.html
node -e 'const fs=require("fs");const html=fs.readFileSync("example/report.html","utf8");const scripts=[...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].map(m=>m[1]).join("\n");new Function(scripts);console.log("script syntax ok")'
```

## Next recommended fixes

1. Make `optimizedReportTemplate()` idempotent and add a test that rendering the template multiple times does not duplicate browser calls.
2. Decide whether the folder icon should stay remote or be embedded to preserve offline/self-contained behavior.
3. Split browser source from generated sample output so changes are edited in source files rather than in generated HTML.
4. Add a generated-report smoke test that verifies the HTML contains exactly one `scheduleSearchCandidateIndexBuild();` call in `initReport`.
