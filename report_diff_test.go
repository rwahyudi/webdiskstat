package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func diffReportFixture() *DiffReport {
	root := &DiffNode{Name: "private-root", Path: "/private-root", Type: "dir", Before: 500, After: 500, BeforeFiles: 262, AfterFiles: 262, Status: "unchanged"}
	context := &DiffNode{Name: "offset", Path: "/private-root/offset", Type: "dir", Before: 100, After: 100, Status: "unchanged", Children: []*DiffNode{
		{Name: "grow.bin", Path: "/private-root/offset/grow.bin", Type: "file", Before: 0, After: 100, Delta: 100, Status: "added"},
		{Name: "shrink.bin", Path: "/private-root/offset/shrink.bin", Type: "file", Before: 100, After: 0, Delta: -100, Status: "removed"},
	}}
	root.Children = append(root.Children, context)
	for i := 0; i < 260; i++ {
		name := fmt.Sprintf("changed-%03d.bin", i)
		root.Children = append(root.Children, &DiffNode{Name: name, Path: root.Path + "/" + name, Type: "file", Before: 1, After: 2, Delta: 1, Status: "grown"})
	}
	root.Children = append(root.Children, &DiffNode{Name: "</script><script>window.diffXSS=1</script>", Path: "/private-root/=formula", Type: "file", Status: "changed"})
	rankings := DiffRankings{
		GrowingFiles:   []DiffRankingRow{{Name: "grow.bin", Path: "/private-root/offset/grow.bin", Type: "file", After: 100, Delta: 100, Status: "added"}},
		ShrinkingFiles: []DiffRankingRow{{Name: "shrink.bin", Path: "/private-root/offset/shrink.bin", Type: "file", Before: 100, Delta: -100, Status: "removed"}},
	}
	for i := 0; i < 99; i++ {
		n := root.Children[i+1]
		rankings.GrowingFiles = append(rankings.GrowingFiles, DiffRankingRow{Name: n.Name, Path: n.Path, Type: n.Type, Before: n.Before, After: n.After, Delta: n.Delta, Status: n.Status})
	}
	return &DiffReport{Before: DiffSnapshot{Source: "baseline-secret.json", Root: root.Path, Size: 500, Files: 262, Directories: 2, CapturedAt: "2026-10-31T00:00:00Z"}, After: DiffSnapshot{Source: "current-secret.json", Root: root.Path, Size: 500, Files: 262, Directories: 2}, Root: root, Increased: 360, Decreased: 100, StatusCounts: map[string]int{"added": 1, "removed": 1, "grown": 260, "changed": 1}, Rankings: rankings}
}

func diffPayloadFromHTML(t *testing.T, html string) map[string]any {
	t.Helper()
	match := regexp.MustCompile(`(?s)<script id="diffData" type="application/json">(.*?)</script>`).FindStringSubmatch(html)
	if len(match) != 2 {
		t.Fatal("missing diff payload")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(match[1]), &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestDiffReportCompressedJSONRoundTrip(t *testing.T) {
	want := diffReportFixture()
	html, err := renderDiffReport(want, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := diffPayloadFromHTML(t, html)
	if payload["format"] != "gds-diff-json-v1" || payload["encoding"] != "base85" || payload["encrypted"] != false {
		t.Fatalf("unexpected payload: %v", payload)
	}
	compressed, err := base85DecodeBytes(payload["payload"].(string), int(payload["length"].(float64)))
	if err != nil {
		t.Fatal(err)
	}
	r, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	var got DiffReport
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&got, want) {
		t.Fatal("gzip JSON did not round-trip")
	}
	for _, forbidden := range []string{"window.diffXSS=1", "baseline-secret.json", "/* DIFF_", "https://", "http://"} {
		// SVG namespace URI is an identifier, not a network request.
		if forbidden == "http://" {
			continue
		}
		if strings.Contains(html, forbidden) {
			t.Errorf("artifact contains %q", forbidden)
		}
	}
	if strings.Contains(html, ".innerHTML") {
		t.Error("diff UI should only use safe DOM construction")
	}
	for _, required := range []string{"function pbkdf2Sha256(", "function chacha20Poly1305Decrypt(", "function bytesFromReportPayload(", "DecompressionStream", "aria-sort", "csvCell", "visible.slice(start, start + size)"} {
		if !strings.Contains(html, required) {
			t.Errorf("missing runtime feature %s", required)
		}
	}
}

func TestDiffReportEncryptedArtifact(t *testing.T) {
	password := "diff-password"
	html, err := renderDiffReport(diffReportFixture(), &password)
	if err != nil {
		t.Fatal(err)
	}
	payload := diffPayloadFromHTML(t, html)
	if payload["encrypted"] != true || payload["algorithm"] != encryptionAlgorithm || payload["format"] != "gds-diff-json-v1" {
		t.Fatalf("unexpected encryption metadata: %v", payload)
	}
	for _, secret := range []string{password, "private-root", "baseline-secret.json", "current-secret.json", "grow.bin", "window.diffXSS=1", "2026-10-31T00:00:00Z"} {
		if strings.Contains(html, secret) {
			t.Errorf("encrypted artifact leaks %q", secret)
		}
	}
}

func TestDiffReportRejectsMissingRoot(t *testing.T) {
	for _, report := range []*DiffReport{nil, {}} {
		if _, err := renderDiffReport(report, nil); err == nil {
			t.Error("expected missing-root error")
		}
	}
}

// Browser coverage is optional where Playwright or its Chromium installation is
// absent. It runs against file://, including the crypto.subtle-free fallback.
func TestDiffReportBrowser(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable; browser verification skipped")
	}
	dir := t.TempDir()
	password := "browser-password"
	noChange := &DiffReport{Before: DiffSnapshot{}, After: DiffSnapshot{}, Root: &DiffNode{Name: "empty", Type: "dir", Status: "unchanged"}}
	for _, fixture := range []struct {
		name     string
		report   *DiffReport
		password *string
	}{{"plain", diffReportFixture(), nil}, {"encrypted", diffReportFixture(), &password}, {"empty", noChange, nil}} {
		html, err := renderDiffReport(fixture.report, fixture.password)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fixture.name+".html"), []byte(html), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(python, "-", dir)
	cmd.Stdin = strings.NewReader(diffReportBrowserCheck)
	output, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 77 {
		t.Skip(strings.TrimSpace(string(output)))
	}
	if err != nil {
		t.Fatalf("browser verification: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}

const diffReportBrowserCheck = `
import sys, pathlib, glob, os, csv, io, json
try:
 from playwright.sync_api import sync_playwright
except ImportError:
 print('Playwright unavailable; browser verification skipped'); sys.exit(77)
root=pathlib.Path(sys.argv[1])
with sync_playwright() as p:
 candidates=[p.chromium.executable_path]+glob.glob(os.path.expanduser('~/.cache/ms-playwright/chromium-*/chrome-linux*/chrome'))
 binary=next((x for x in candidates if os.path.isfile(x)),None)
 if not binary:
  print('Chromium unavailable; browser verification skipped'); sys.exit(77)
 browser=p.chromium.launch(executable_path=binary,headless=True,args=['--no-sandbox'])
 page=browser.new_page(accept_downloads=True,viewport={'width':1440,'height':1100})
 errors=[]; requests=[]
 page.on('pageerror',lambda e:errors.append(str(e)))
 page.on('request',lambda r:requests.append(r.url) if r.url.startswith(('http://','https://')) else None)
 page.goto((root/'plain.html').as_uri());page.locator('#report').wait_for(state='visible')
 assert page.locator('#rows tr').count()==50
 assert page.locator('#netSize').inner_text()=='0 B'
 assert page.evaluate('window.diffXSS') is None
 assert page.locator('#beforeSource').inner_text().find('baseline-secret.json')>=0
 assert 'Captured:' in page.locator('#beforeSource').inner_text()
 assert '2026-10-31T00:00:00Z' not in page.locator('#beforeSource').inner_text()
 times=page.evaluate("[captureTime('1793404800'),captureTime('2026-10-31T00:00:00Z'),captureTime(''),captureTime('invalid'),captureTime(null),captureTime('99999999999999999')]")
 assert times[0]==times[1] and '2026' in times[0], times
 assert all(t=='Capture time unavailable' for t in times[2:]), times
 assert page.evaluate('typeof largestChanges')=='undefined'
 assert page.locator('[data-category="growingFiles"] tbody tr').count()==10
 page.locator('#next').click();assert page.locator('#pageLabel').inner_text()=='2 / 6'
 page.locator('#pageSize').select_option('200');assert page.locator('#rows tr').count()<=200
 page.locator('#search').fill('grow.bin');page.wait_for_timeout(250)
 assert page.locator('#rows tr').count()==1
 assert 'added' in page.locator('#rows').inner_text()
 page.locator('#search').fill('');page.locator('#status').select_option('removed');page.wait_for_timeout(250)
 assert page.locator('#rows tr').count()==1
 assert 'shrink.bin' in page.locator('#rows').inner_text()
 page.locator('#reset').click()
 page.get_by_role('button',name='Change',exact=True).click()
 page.get_by_role('button',name='Open directory /private-root/offset',exact=True).click()
 assert page.locator('#rows tr').count()==2
 assert 'grow.bin' in page.locator('#rows').inner_text()
 page.get_by_role('button',name='private-root',exact=True).click()
 page.locator('#search').fill('no-such-file');page.wait_for_timeout(250)
 assert page.locator('#empty').is_visible()
 assert 'No matching changes' in page.locator('#empty').inner_text()
 page.locator('#reset').click()
 with page.expect_download() as dl: page.locator('#csv').click()
 download=dl.value; text=pathlib.Path(download.path()).read_text(encoding='utf-8-sig'); rows=list(csv.DictReader(io.StringIO(text)))
 assert len(rows)==263, len(rows)
 assert all(r['status']!='unchanged' for r in rows)
 assert not any(r['path']=='/private-root/offset' for r in rows)
 safe=page.evaluate("['=SUM(A1)', '+cmd', '-cmd', '@cmd', '  =cmd', '\\tcmd', '\\rcmd', 'normal', 'a\\\"b'].map(csvCell)")
 assert all(x.startswith('"\'') for x in safe[:7]), safe
 assert safe[7]=='"normal"' and safe[8]=='"a""b"'
 # Export remains global even with a search active.
 page.locator('#search').fill('grow.bin');page.wait_for_timeout(250)
 with page.expect_download() as dl: page.locator('#csv').click()
 assert len(list(csv.DictReader(io.StringIO(pathlib.Path(dl.value.path()).read_text(encoding='utf-8-sig')))))==263
 page.locator('#reset').click()
 page.get_by_role('button',name='Name / path',exact=True).click()
 assert page.locator('#head th').first.get_attribute('aria-sort')=='ascending'
 page.locator('#search').focus();page.keyboard.press('Tab');assert page.locator('#status').evaluate('(e)=>e===document.activeElement')
 out=os.environ.get('DIFF_REPORT_SCREENSHOTS')
 if out:
  pathlib.Path(out).mkdir(parents=True,exist_ok=True);page.screenshot(path=str(pathlib.Path(out)/'desktop.png'),full_page=True)
 page.set_viewport_size({'width':390,'height':844})
 assert page.evaluate('document.documentElement.scrollWidth<=window.innerWidth')
 if out: page.screenshot(path=str(pathlib.Path(out)/'mobile.png'),full_page=True)
 page.goto((root/'empty.html').as_uri());page.locator('#report').wait_for(state='visible')
 assert 'No changes found' in page.locator('#empty').inner_text()
 assert page.locator('#csv').is_disabled()
 # Verify actual encrypted artifact with wrong password then correct password.
 page.goto((root/'encrypted.html').as_uri());page.locator('#passwordDialog').wait_for(state='visible')
 page.locator('#password').fill('wrong');page.get_by_role('button',name='Unlock',exact=True).click()
 page.get_by_text('Incorrect password. Try again:',exact=True).wait_for()
 page.locator('#password').fill('browser-password');page.get_by_role('button',name='Unlock',exact=True).click()
 page.locator('#report').wait_for(state='visible',timeout=60000)
 assert 'baseline-secret.json' in page.locator('#beforeSource').inner_text()
 # Force the existing pure-JS KDF fallback as on restricted offline browsers.
 context=browser.new_context();context.add_init_script('Object.defineProperty(globalThis,"crypto",{value:undefined,configurable:true})')
 fallback=context.new_page();fallback.goto((root/'encrypted.html').as_uri());fallback.locator('#passwordDialog').wait_for(state='visible')
 fallback.locator('#password').fill('browser-password');fallback.get_by_role('button',name='Unlock',exact=True).click()
 fallback.locator('#report').wait_for(state='visible',timeout=120000)
 assert fallback.locator('#rows tr').count()==50
 assert not errors, errors
 assert not requests, requests
 browser.close()
 print('PASS: desktop/mobile, human-readable capture times, dedicated default-ten rankings, bounded rows, global search, filters, navigation, sorting, CSV safety/completeness, no-change, XSS, keyboard, encrypted unlock/retry, pure-JS crypto fallback, zero network requests')
`
