package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiskDiffPipelineBrowser(t *testing.T) {
	dir := t.TempDir()
	before := `[1,2,{"progname":"gdu","timestamp":1700000000},[{"name":"/data","dsize":4},[{"name":"stable","dsize":4},{"name":"same.txt","dsize":8}],{"name":"change.txt","dsize":16}]]`
	after := `[1,2,{"progname":"gdu","timestamp":1700000010},[{"name":"/data","dsize":4},[{"name":"stable","dsize":4},{"name":"same.txt","dsize":8}],{"name":"change.txt","dsize":32}]]`
	beforePath, afterPath := filepath.Join(dir, "before.json"), filepath.Join(dir, "after.json")
	for name, data := range map[string]string{beforePath: before, afterPath: after} {
		if err := os.WriteFile(name, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pages := make(map[string]string)
	for _, mode := range []string{"changes", "complete", "encrypted"} {
		args := []string{"--diff", beforePath, afterPath, "-o", "-"}
		if mode != "changes" {
			args = append(args, "-c")
		}
		if mode == "encrypted" {
			args = append(args, "--password", "pipeline-password")
		}
		var stdout, stderr bytes.Buffer
		if code := run(args, strings.NewReader(""), &stdout, &stderr); code != 0 {
			t.Fatalf("%s pipeline exit=%d: %s", mode, code, stderr.String())
		}
		assertDiffFooterMetadata(t, stdout.String())
		pages[mode] = stdout.String()
	}
	runDiffBrowserPython(t, diffPipelineBrowserCheck, pages)
}

const diffPipelineBrowserCheck = diffBrowserPythonPrelude + `
page=new_page();load(page,'changes')
assert page.locator('#diffMode').inner_text()=='Diff mode: Changes only'
assert page.locator('#tree .row[title="/data/stable"]').count()==0
assert page.locator('#tree .row[title="/data/change.txt"]').count()==1
for mode in ['complete','encrypted']:
 if mode=='encrypted':
  page.goto((root/'encrypted.html').as_uri());page.locator('#passwordDialog').wait_for(state='visible')
  page.locator('#password').fill('pipeline-password');page.get_by_role('button',name='Unlock',exact=True).click()
  page.locator('#tree .tree-header').wait_for(state='visible',timeout=60000)
 else:load(page,mode)
 assert page.locator('#diffMode').inner_text()=='Diff mode: Complete'
 page.locator('#tree .row[title="/data/stable"]').dblclick()
 assert page.locator('#tree .row[title="/data/stable/same.txt"]').count()==1
 with page.expect_download() as download:page.locator('#csv').click()
 records=list(csv.DictReader(io.StringIO(pathlib.Path(download.value.path()).read_text(encoding='utf-8-sig'))))
 assert any(record['path']=='/data/change.txt' for record in records),records
 assert all(record['status']!='unchanged' for record in records),records
finish('PASS: streamed CLI reports open offline in both modes and encrypted form; unchanged navigation, CSV, footer metadata')
`

func TestDiskDiffPipelineFailurePreservesDestination(t *testing.T) {
	dir := t.TempDir()
	beforePath, afterPath, output := filepath.Join(dir, "before.json"), filepath.Join(dir, "bad.json"), filepath.Join(dir, "report.html")
	for name, data := range map[string]string{
		beforePath: `[1,2,{"progname":"gdu"},[{"name":"/data"}]]`,
		afterPath:  `[1,2,{"progname":"gdu"},[{"name":"/data"}]] trailing`,
		output:     "previous complete report",
	} {
		if err := os.WriteFile(name, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--diff", "-c", beforePath, afterPath, "-o", output}, strings.NewReader(""), &stdout, &stderr); code == 0 {
		t.Fatal("malformed comparison unexpectedly succeeded")
	}
	contents, err := os.ReadFile(output)
	if err != nil || string(contents) != "previous complete report" || stdout.Len() != 0 {
		t.Fatalf("failed pipeline altered output: %q, stdout=%q, err=%v", contents, stdout.String(), err)
	}
}
