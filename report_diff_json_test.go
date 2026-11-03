package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestDiffJSONRuntimeStreamsAndValidates(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable")
	}
	if !strings.Contains(diffReportTemplate, "data = await parseDiffJSONStream(stream)") || strings.Contains(diffReportTemplate, "new Response(stream).text()") {
		t.Fatal("diff report boot must parse the decompressed stream incrementally")
	}
	harness := `
const assert = require('node:assert/strict');
function streamOf(chunks) {
  let i = 0;
  return new ReadableStream({ pull(controller) { if (i < chunks.length) controller.enqueue(chunks[i++]); else controller.close(); } });
}
(async () => {
  const source = '{"s":"hi \\"quoted\\" \\\\ \\u263A 🐈","arr":[-1.2e+3,true,false,null,{"__proto__":{"polluted":1},"x":1}],"dup":1,"dup":2,"lone":"\\uD800"}';
  const bytes = new TextEncoder().encode(source);
  const oneByte = Array.from(bytes, b => Uint8Array.of(b));
  const actual = await parseDiffJSONStream(streamOf(oneByte));
  const expected = JSON.parse(source);
  assert.deepEqual(actual, expected);
  assert.equal(Object.hasOwn(actual.arr[4], '__proto__'), true);
  assert.equal({}.polluted, undefined);
  assert.equal(actual.dup, 2);

  for (const bad of ['{"a":', '1 2', '01', '[1,]', '"\\x"', 'truex', '{"a":1']) {
    await assert.rejects(parseDiffJSONStream(streamOf([new TextEncoder().encode(bad)])));
  }

  // Produce a multi-megabyte report in bounded chunks; reject accidental
  // whole-report JSON.parse while still permitting token parsing.
  const originalParse = JSON.parse;
  JSON.parse = text => { if (text.length > 65536) throw new Error('whole-report JSON.parse'); return originalParse(text); };
  const count = 150000;
  let emitted = 0;
  let opened = false;
  const large = new ReadableStream({
    pull(controller) {
      if (!opened) { opened = true; controller.enqueue(new TextEncoder().encode('{"nodes":[')); return; }
      const end = Math.min(emitted + 512, count);
      const part = [];
      for (; emitted < end; emitted++) part.push((emitted ? ',' : '') + '{"name":"node","size":123}');
      controller.enqueue(new TextEncoder().encode(part.join('')));
      if (emitted === count) { controller.enqueue(new TextEncoder().encode(']}')); controller.close(); }
    }
  });
  const big = await parseDiffJSONStream(large);
  assert.equal(big.nodes.length, count);
  assert.equal(big.nodes[count - 1].size, 123);
})().catch(error => { console.error(error); process.exitCode = 1; });
`
	command := exec.Command(node, "-e", diffJSONRuntime+"\n"+harness)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("incremental JSON runtime failed: %v\n%s", err, output)
	}
}
