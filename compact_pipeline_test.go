package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompactPipelineBrowser(t *testing.T) {
	dir := t.TempDir()
	before := `[1,2,{"progname":"gdu"},[{"name":"/data","dsize":4},[{"name":"stable","dsize":4},{"name":"same.txt","dsize":8}],{"name":"change.txt","dsize":16}]]`
	after := strings.Replace(before, `"dsize":16`, `"dsize":32`, 1)
	b, a := filepath.Join(dir, "before.json"), filepath.Join(dir, "after.json")
	for path, content := range map[string]string{b: before, a: after} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	pages := make(map[string]string)
	for _, mode := range []string{"changes", "complete", "encrypted", "identical"} {
		current := a
		if mode == "identical" {
			current = b
		}
		args := []string{"--diff", b, current, "-o", "-"}
		if mode != "changes" && mode != "identical" {
			args = append(args, "-c")
		}
		if mode == "encrypted" {
			args = append(args, "--password", "compact-test-password")
		}
		var out, stderr bytes.Buffer
		if code := run(args, strings.NewReader(""), &out, &stderr); code != 0 {
			t.Fatalf("%s: exit %d: %s", mode, code, stderr.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("%s: stdout HTML received extra stderr: %s", mode, stderr.String())
		}
		assertDiffFooterMetadata(t, out.String())
		pages[mode] = out.String()
	}
	runDiffBrowserPython(t, compactPipelineBrowserCheck, pages)
}

const compactPipelineBrowserCheck = diffBrowserPythonPrelude + `
page=new_page();load(page,'changes')
assert page.locator('#diffMode').inner_text()=='Diff mode: Changes only'
assert page.locator('#tree .row[title="/data/stable"]').count()==0
assert page.locator('#tree .row[title="/data/change.txt"]').count()==1
for mode in ['complete','encrypted']:
 if mode=='encrypted':
  page.goto((root/'encrypted.html').as_uri());page.locator('#passwordDialog').wait_for(state='visible')
  page.locator('#password').fill('compact-test-password');page.get_by_role('button',name='Unlock',exact=True).click()
  page.locator('#tree .tree-header').wait_for(state='visible',timeout=60000)
 else:load(page,mode)
 assert page.locator('#diffMode').inner_text()=='Diff mode: Complete'
 page.locator('#tree .row[title="/data/stable"]').dblclick()
 assert page.locator('#tree .row[title="/data/stable/same.txt"]').count()==1
 with page.expect_download() as download:page.locator('#csv').click()
 records=list(csv.DictReader(io.StringIO(pathlib.Path(download.value.path()).read_text(encoding='utf-8-sig'))))
 assert any(record['path']=='/data/change.txt' for record in records),records
 assert all(record['status']!='unchanged' for record in records),records
load(page,'identical')
assert page.locator('#diffMode').inner_text()=='Diff mode: Changes only'
assert page.locator('#tree .row[title="/data/stable"]').count()==0
finish('PASS: compact CLI pipeline boots offline, complete retains unchanged navigation, encrypted unlock and CSV succeed, identical comparison boots')
`
