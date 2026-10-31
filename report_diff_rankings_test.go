package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func diffRankingsFixture() *DiffReport {
	report := diffReportFixture()
	sets := []struct {
		key    string
		kind   string
		growth bool
		rows   *[]DiffRankingRow
	}{
		{"growingFiles", "file", true, &report.Rankings.GrowingFiles},
		{"shrinkingFiles", "file", false, &report.Rankings.ShrinkingFiles},
		{"growingDirectories", "dir", true, &report.Rankings.GrowingDirectories},
		{"shrinkingDirectories", "dir", false, &report.Rankings.ShrinkingDirectories},
	}
	for _, set := range sets {
		*set.rows = nil
		for i := 0; i < 100; i++ {
			name := fmt.Sprintf("%s-%03d", set.key, i)
			delta := int64(100-i) * 1024 * 1024
			before, after, status := int64(0), delta, "added"
			if !set.growth {
				before, after, delta, status = delta, 0, -delta, "removed"
			}
			*set.rows = append(*set.rows, DiffRankingRow{Name: name, Path: "/ranking-only/" + name, Type: set.kind, Before: before, After: after, Delta: delta, Status: status})
		}
	}
	// These flat records resolve into actual browser objects only when clicked.
	// They deliberately precede larger values to catch any client-side sorting.
	report.Rankings.GrowingFiles[0] = DiffRankingRow{Name: "grow.bin", Path: "/private-root/offset/grow.bin", Type: "file", After: 100, Delta: 100, Status: "added"}
	report.Rankings.GrowingDirectories[0] = DiffRankingRow{Name: "offset", Path: "/private-root/offset", Type: "dir", Before: 100, After: 100, Status: "unchanged"}
	return report
}

func TestDiffReportRankingsBrowser(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable; rankings browser verification skipped")
	}
	dir := t.TempDir()
	password := "rankings-password"
	empty := diffReportFixture()
	empty.Rankings = DiffRankings{}
	for _, fixture := range []struct {
		name     string
		report   *DiffReport
		password *string
	}{{"plain", diffRankingsFixture(), nil}, {"encrypted", diffRankingsFixture(), &password}, {"empty", empty, nil}} {
		html, err := renderDiffReport(fixture.report, fixture.password)
		if err != nil {
			t.Fatal(err)
		}
		if fixture.password != nil && (strings.Contains(html, "/ranking-only/") || strings.Contains(html, "growingFiles-001")) {
			t.Fatal("encrypted artifact exposes ranking dataset")
		}
		if err := os.WriteFile(filepath.Join(dir, fixture.name+".html"), []byte(html), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(python, "-", dir)
	cmd.Stdin = strings.NewReader(diffRankingsBrowserCheck)
	output, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 77 {
		t.Skip(strings.TrimSpace(string(output)))
	}
	if err != nil {
		t.Fatalf("rankings browser verification: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}

const diffRankingsBrowserCheck = `
import sys,pathlib,glob,os
try:
 from playwright.sync_api import sync_playwright
except ImportError:
 print('Playwright unavailable; rankings browser verification skipped');sys.exit(77)
root=pathlib.Path(sys.argv[1]);out=os.environ.get('DIFF_RANKINGS_SCREENSHOTS')
if out:pathlib.Path(out).mkdir(parents=True,exist_ok=True)
with sync_playwright() as p:
 binary=next((s for s in [p.chromium.executable_path]+glob.glob(os.path.expanduser('~/.cache/ms-playwright/chromium-*/chrome-linux*/chrome')) if os.path.isfile(s)),None)
 if not binary:print('Chromium unavailable; rankings browser verification skipped');sys.exit(77)
 browser=p.chromium.launch(executable_path=binary,headless=True,args=['--no-sandbox'])
 page=browser.new_page(viewport={'width':1440,'height':1100});errors=[];network=[]
 page.on('pageerror',lambda e:errors.append(str(e)))
 page.on('request',lambda r:network.append(r.url) if r.url.startswith(('http://','https://')) else None)
 page.goto((root/'plain.html').as_uri());page.locator('#report').wait_for(state='visible')
 assert page.locator('h1').count()==0
 assert 'What changed on disk?' not in page.locator('body').inner_text()
 assert 'Compare snapshot metadata, not file contents.' not in page.locator('body').inner_text()
 assert page.locator('script[type="application/json"]').count()==1
 assert page.evaluate('typeof largestChanges')=='undefined'
 categories=['growingFiles','shrinkingFiles','growingDirectories','shrinkingDirectories']
 def counts():return [page.locator('[data-category="'+k+'"] tbody tr').count() for k in categories]
 assert page.locator('#rankLimit').input_value()=='10' and counts()==[10]*4
 assert page.locator('[data-category="growingFiles"] tbody button').first.inner_text()=='grow.bin'
 assert page.locator('[data-category="growingFiles"] tbody button').nth(1).inner_text()=='growingFiles-001'
 def geometry():
  sections=[page.locator('#summarySection').bounding_box(),page.locator('#rankingsSection').bounding_box(),page.locator('section[aria-labelledby="explorerTitle"]').bounding_box()]
  assert all(abs(x['x']-sections[0]['x'])<1 and abs(x['width']-sections[0]['width'])<1 for x in sections),sections
  assert all(a['y']+a['height']<=b['y'] for a,b in zip(sections,sections[1:])),sections
  assert page.evaluate('document.documentElement.scrollWidth<=window.innerWidth')
 for limit in ['10','25','50','100']:
  page.locator('#rankLimit').select_option(limit);assert counts()==[int(limit)]*4
  geometry()
  assert page.locator('#pageSize').input_value()=='50'
 page.locator('#rankLimit').select_option('25');original=page.locator('#rankings').inner_html()
 page.locator('#search').fill('grow.bin');page.wait_for_timeout(250)
 assert page.locator('#rows tr').count()==1 and page.locator('#rankings').inner_html()==original
 page.locator('#search').fill('');page.locator('#status').select_option('removed');page.wait_for_timeout(250)
 assert page.locator('#rankings').inner_html()==original
 page.locator('#pageSize').select_option('100');page.locator('#reset').click()
 page.get_by_role('button',name='Name / path',exact=True).click()
 assert page.locator('#rankLimit').input_value()=='25' and page.locator('#rankings').inner_html()==original
 # A detached ranking file must navigate to the actual tree parent, not root.
 page.locator('[data-category="growingFiles"] button').first.click()
 assert page.locator('#rows tr').count()==2
 assert 'offset' in page.locator('#crumbs').inner_text()
 page.locator('#reset').click()
 page.locator('[data-category="growingDirectories"] button').first.click()
 assert page.locator('#rows tr').count()==2
 page.locator('#reset').click();page.locator('#pageSize').select_option('50')
 for theme in ['dark','light']:
  page.evaluate('(theme)=>applyTheme(theme,true)',theme);page.wait_for_timeout(250)
  for device,viewport in [('desktop',{'width':1440,'height':1100}),('mobile',{'width':390,'height':844})]:
   page.set_viewport_size(viewport);page.evaluate('window.scrollTo(0,0)')
   page.locator('#rankLimit').select_option('10');geometry()
   assert page.locator('.ranking-table td:last-child').evaluate_all('(cells)=>cells.every(e=>e.scrollWidth<=e.clientWidth)'), 'ranking deltas must not be clipped'
   if out:page.screenshot(path=str(pathlib.Path(out)/(device+'-'+theme+'.png')))
   page.locator('#rankLimit').select_option('100');geometry()
   assert counts()==[100]*4
   assert page.locator('.ranking-scroll').first.evaluate('(e)=>e.scrollHeight>e.clientHeight')
   page.locator('#rankLimit').select_option('10')
   if device=='mobile' and out:
    page.locator('#rankingsSection').evaluate('(e)=>window.scrollTo(0,e.getBoundingClientRect().top+window.scrollY)')
    page.screenshot(path=str(pathlib.Path(out)/('mobile-rankings-'+theme+'.png')))
 # Neither mutable explorer arrays nor the browser tree can become a fallback.
 page.evaluate('changed=[];visible=[];data.root.children=[];current=data.root;refresh();rankings()')
 assert counts()==[10]*4 and page.locator('#rows tr').count()==0
 page.locator('[data-category="growingFiles"] button').first.click()
 assert page.locator('#rankingMessage').is_visible()
 # A defensive DOM cap still applies if malformed payloads exceed 100.
 page.evaluate('for(const rows of Object.values(data.rankings))rows.push(...rows);rankings()')
 page.locator('#rankLimit').select_option('100');assert counts()==[100]*4
 page.goto((root/'empty.html').as_uri());page.locator('#report').wait_for(state='visible')
 assert page.locator('#rows tr').count()==50
 assert page.locator('#rankings button').count()==0
 assert page.locator('.ranking-empty').count()==4
 for limit in ['10','25','50','100']:
  page.locator('#rankLimit').select_option(limit);assert page.locator('#rankings button').count()==0
 # Both logical datasets remain inside one encrypted envelope and one unlock.
 page.goto((root/'encrypted.html').as_uri());page.locator('#passwordDialog').wait_for(state='visible')
 page.locator('#password').fill('rankings-password');page.get_by_role('button',name='Unlock',exact=True).click()
 page.locator('#report').wait_for(state='visible',timeout=60000)
 assert counts()==[10]*4 and page.locator('#rows tr').count()==50
 page.locator('#rankLimit').select_option('100');assert counts()==[100]*4
 assert not errors,errors
 assert not network,network
 browser.close()
 print('PASS: standalone full-width summary/rankings/browser in both themes desktop/mobile, no intro, one embedded rankings dataset, default10/25/50/100, bounded100 DOM, empty-no-fallback, source order, explorer independence, lazy path navigation, single encrypted unlock, zero network requests')
`
