package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestDiffBinaryDecoderMatchesCompactJSON(t *testing.T) {
	report := &compactDiffReport{
		Before:   DiffSnapshot{Source: "before", Root: "/", Size: 21, Files: 2, Directories: 1, CapturedAt: "2026-10-01T00:00:00Z"},
		After:    DiffSnapshot{Source: "after", Root: "/", Size: 35, Files: 2, Directories: 1, CapturedAt: "2026-10-02T00:00:00Z"},
		Complete: true, Increased: 14, StatusCounts: map[string]int{"added": 1, "removed": 1, "grown": 1, "shrunk": 0, "changed": 0, "unchanged": 0},
		Count: 4, root: 0,
		rows: []compactDiffRow{
			{name: "root", fullPath: "/", before: 21, after: 35, beforeFiles: 2, afterFiles: 2, beforeItems: 3, afterItems: 3, firstChild: 1, beforeMTime: "", afterMTime: "root-time", beforeType: compactTypeDir, afterType: compactTypeDir, typ: compactTypeDir, status: compactStatusGrown},
			{name: "unchanged-📄", fullPath: "/unchanged-📄", before: 7, after: 7, beforeFiles: 1, afterFiles: 1, beforeItems: 1, afterItems: 1, beforeMTime: "old", afterMTime: "old", firstChild: -1, nextSibling: 2, beforeType: compactTypeFile, afterType: compactTypeFile, typ: compactTypeFile, status: compactStatusUnchanged},
			{name: "removed", fullPath: "/removed", before: 8, beforeFiles: 1, beforeItems: 1, firstChild: -1, nextSibling: 3, beforeType: compactTypeFile, typ: compactTypeFile, status: compactStatusRemoved},
			{name: "grown", fullPath: "/grown", before: 6, after: 20, beforeFiles: 1, afterFiles: 1, beforeItems: 1, afterItems: 1, firstChild: -1, nextSibling: -1, beforeMTime: "old", afterMTime: "new", beforeType: compactTypeFile, afterType: compactTypeFile, typ: compactTypeFile, status: compactStatusGrown},
		},
	}
	var binary, legacy bytes.Buffer
	if err := report.WriteBinary(&binary); err != nil {
		t.Fatal(err)
	}
	if err := report.WriteJSON(&legacy); err != nil {
		t.Fatal(err)
	}
	var expected any
	if err := json.Unmarshal(legacy.Bytes(), &expected); err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	script := diffBinaryRuntime + `
const assert = require("node:assert/strict");
const bytes = Buffer.from(process.argv[1], "base64");
const expected = JSON.parse(Buffer.from(process.argv[2], "base64").toString("utf8"));
assert.deepStrictEqual(decodeDiffBinary(bytes), expected);
for (const bad of [bytes.subarray(0, bytes.length - 1), Buffer.from("NOPE"), Buffer.from([0x47,0x44,0x44,0x02,0x80])]) {
  assert.throws(() => decodeDiffBinary(bad), /invalid diff binary/);
}
const uv = n => { const out = []; n = BigInt(n); do { let b = Number(n & 127n); n >>= 7n; if (n) b |= 128; out.push(b); } while (n); return out; };
const pack = (stringBytes, records, overlays, metadata = [123,125]) => Buffer.from([
  0x47,0x44,0x44,0x02, ...uv(metadata.length), ...metadata,
  ...uv(stringBytes.length), ...stringBytes.flatMap(s => [...uv(s.length), ...s]),
  ...uv(records.length), ...records.flatMap(record => record.flatMap(uv)),
  ...uv(overlays.length), ...overlays.flatMap(record => record.flatMap(uv))
]);
const validRecords = [[1,0,2,0,0,0,0,1],[1,0,1,1,1,1,0,0]];
const oneOverlay = [0,1,0,0,0,0,1];
assert.throws(() => decodeDiffBinary(pack([[114,111,111,116]], [[2,0,2,0,0,0,0,0]], [])), /string reference/);
assert.throws(() => decodeDiffBinary(pack([[255]], [[1,0,1,0,0,0,0,0]], [])), /UTF-8/);
assert.throws(() => decodeDiffBinary(pack([[114,111,111,116]], validRecords, [oneOverlay, oneOverlay])), /strictly increasing/);
assert.throws(() => decodeDiffBinary(pack([[114,111,111,116]], [[1,0,1,0,0,0,0,0],[1,0,1,0,0,0,0,0]], [])), /more than one root/);
assert.throws(() => decodeDiffBinary(pack([[114,111,111,116]], [[1,0,2,0,0,0,0,2],[1,0,1,0,0,0,0,0]], [])), /base node record/);
assert.throws(() => decodeDiffBinary(pack([[114,111,111,116]], validRecords, [[2,1,0,0,0,0,1]])), /strictly increasing/);
assert.throws(() => decodeDiffBinary(Buffer.concat([pack([[114,111,111,116]], validRecords, []), Buffer.from([0])])), /trailing bytes/);
assert.throws(() => decodeDiffBinary(Buffer.from([0x47,0x44,0x44,0x02,...uv(16*1024*1024+1)])), /metadata length/);
const scalarMax = (1n << 64n) - 1n;
const maxScalarTree = decodeDiffBinary(pack([[114,111,111,116]], [[1,0,1,scalarMax,0,0,0,0]], []));
assert.equal(maxScalarTree.root.after, Number(scalarMax));
assert.throws(() => decodeDiffBinary(pack([[114,111,111,116]], [[1,0,1,0,5000001,0,0,0]], [])), /after node counts/);
assert.throws(() => decodeDiffBinary(pack([[114,111,111,116]], [[1,0,1,0,0,0,0,0]], [[0,1,0,0,5000001,0,0]])), /before node counts/);
const structuralOverflow = Buffer.from([0x47,0x44,0x44,0x02,...uv(2),123,125,...uv(0),...uv(9007199254740992n)]);
assert.throws(() => decodeDiffBinary(structuralOverflow), /safe integer range/);
assert.throws(() => decodeDiffBinary(Buffer.from([0x47,0x44,0x44,0x02,0x80,0x80,0x80,0x80,0x80,0x80,0x80,0x80,0x80,0x02])), /uint64 range/);
assert.throws(() => decodeDiffBinary(Buffer.from([0x47,0x44,0x44,0x02,0x80,0x80])), /truncated varint/);
const deepNodes = Array.from({length:3000}, (_,i)=>[1,0,2,0,0,0,0,i===2999?0:1]);
let deepRoot = decodeDiffBinary(pack([[114]],deepNodes,[])).root;
for(let i=1;i<3000;i++) deepRoot=deepRoot.children[0];
assert.equal(deepRoot.name,'r');
assert.throws(() => decodeDiffBinary(pack([[114]],Array.from({length:4098},(_,i)=>[1,0,2,0,0,0,0,i===4097?0:1]),[])), /nesting limit/);
const overlayTree = decodeDiffBinary(pack(
  [[114,111,111,116],[102,105,108,101],[100,105,114]],
  [[1,0,2,0,0,0,0,3],[2,0,2,5,0,1,3,0],[3,0,2,0,0,3,3,0],[2,0,1,5,1,1,0,0]],
  [[1,1,0,0,0,0,5],[1,2,0,0,2,0,0],[1,0,0,0,0,0,1]]
));
const fileDir = overlayTree.root.children[0];
assert.equal(fileDir.type, "dir");
assert.equal(fileDir.beforeType, "file");
assert.equal(fileDir.before, 0);
assert.equal(fileDir.beforeFiles, 0);
assert.equal(fileDir.beforeItems, 0);
assert.equal(fileDir.afterMtime, "dir");
assert.equal(Object.hasOwn(fileDir, "beforeMtime"), false);
const unchangedDir = overlayTree.root.children[1];
assert.equal(unchangedDir.type, "dir");
assert.equal(unchangedDir.status, "unchanged");
assert.equal(unchangedDir.beforeItems, 2);
assert.equal(unchangedDir.afterItems, 3);
assert.equal(unchangedDir.afterMtime, "dir");
assert.equal(Object.hasOwn(unchangedDir, "beforeMtime"), false);
const added = overlayTree.root.children[2];
assert.equal(added.type, "file");
assert.equal(added.status, "added");
assert.equal(added.before, 0);
assert.equal(added.beforeFiles, 0);
assert.equal(added.beforeItems, 0);
assert.equal(Object.hasOwn(added, "beforeType"), false);
assert.equal(Object.hasOwn(added, "beforeMtime"), false);
`
	cmd := exec.Command("node", "-e", script, binaryString(binary.Bytes()), binaryString(want))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("binary decoder test failed: %v\n%s", err, output)
	}
}

func binaryString(value []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	var out strings.Builder
	for i := 0; i < len(value); i += 3 {
		var n uint32
		remaining := len(value) - i
		n = uint32(value[i]) << 16
		if remaining > 1 {
			n |= uint32(value[i+1]) << 8
		}
		if remaining > 2 {
			n |= uint32(value[i+2])
		}
		out.WriteByte(alphabet[(n>>18)&63])
		out.WriteByte(alphabet[(n>>12)&63])
		if remaining > 1 {
			out.WriteByte(alphabet[(n>>6)&63])
		}
		if remaining > 2 {
			out.WriteByte(alphabet[n&63])
		}
	}
	return out.String()
}
