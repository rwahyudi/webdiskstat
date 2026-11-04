package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiffBinaryBothModesPreserveEveryJSONField(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable")
	}
	cases := []struct{ before, after string }{
		{`[{"name":"/r"},{"name":"removed","dsize":4}]`, `[{"name":"/r"},{"name":"新增.txt","dsize":8}]`},
		{`[{"name":"/r"},[{"name":"same"}]]`, `[{"name":"/r"},[{"name":"same"}],[{"name":"empty"}]]`},
		{`[{"name":"/r"},{"name":"x","dsize":9}]`, `[{"name":"/r"},[{"name":"x","dsize":2},{"name":"child","dsize":3}]]`},
		{`[{"name":"/r"},[{"name":"x","dsize":2},{"name":"child","dsize":3}]]`, `[{"name":"/r"},{"name":"x","dsize":9}]`},
		{`[{"name":"/r"},{"name":"same","dsize":8,"flag":"a"}]`, `[{"name":"/r"},{"name":"same","dsize":8,"flag":"b"}]`},
		{`[{"name":"/r"},{"name":"file","path":"/elsewhere/f","dsize":4,"mtime":12}]`, `[{"name":"/r"},{"name":"file","path":"/elsewhere/f","dsize":4}]`},
		{`[{"name":"/"},[{"name":"empty"}]]`, `[{"name":"/"},[{"name":"empty"}]]`},
	}
	for i, tc := range cases {
		for _, complete := range []bool{false, true} {
			t.Run(fmt.Sprintf("case-%d-complete-%t", i, complete), func(t *testing.T) {
				dir := t.TempDir()
				b, a := filepath.Join(dir, "before.json"), filepath.Join(dir, "after.json")
				for path, tree := range map[string]string{b: tc.before, a: tc.after} {
					if err := os.WriteFile(path, []byte(`[1,2,{"progname":"gdu","timestamp":123},`+tree+`]`), 0600); err != nil {
						t.Fatal(err)
					}
				}
				report, err := buildCompactDiffReport(b, a, strings.NewReader(""), complete)
				if err != nil {
					t.Fatal(err)
				}
				var binary, legacy bytes.Buffer
				if err := report.WriteBinary(&binary); err != nil {
					t.Fatal(err)
				}
				if err := report.WriteJSON(&legacy); err != nil {
					t.Fatal(err)
				}
				script := diffBinaryRuntime + `
const assert=require('node:assert/strict');
const decoded=decodeDiffBinary(Buffer.from(process.argv[1],'base64'));
const expected=JSON.parse(Buffer.from(process.argv[2],'base64').toString('utf8'));
assert.deepStrictEqual(decoded,expected);
`
				cmd := exec.Command(node, "-e", script, base64.StdEncoding.EncodeToString(binary.Bytes()), base64.StdEncoding.EncodeToString(legacy.Bytes()))
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("decoder differs from JSON: %v\n%s", err, out)
				}
			})
		}
	}
}
