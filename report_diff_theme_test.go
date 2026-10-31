package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiffReportSharedAppearance(t *testing.T) {
	html, err := renderDiffReport(diffReportFixture(), nil)
	if err != nil {
		t.Fatal(err)
	}
	css, control, err := diffReportAppearance()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, css) || !strings.Contains(html, control) {
		t.Fatal("comparison must reuse optimized normal palette and switcher")
	}
	initial := strings.Index(html, "localStorage.getItem('webdiskstat-theme')")
	if initial < 0 || initial > strings.Index(html, "<style>") || initial > strings.Index(html, "<body>") {
		t.Fatal("theme restoration must precede first styled paint")
	}
	for _, required := range []string{"color-scheme:dark", "color-scheme:light", "theme-switcher-v2", "aria-checked", "prefers-reduced-motion", "localStorage.setItem(THEME_STORAGE_KEY, normalized)"} {
		if !strings.Contains(html, required) {
			t.Errorf("missing theme feature %s", required)
		}
	}
}

func TestDiffReportThemeBrowser(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable; theme browser verification skipped")
	}
	dir := t.TempDir()
	password := "theme-password"
	for _, fixture := range []struct {
		name     string
		password *string
	}{{"plain", nil}, {"encrypted", &password}} {
		html, err := renderDiffReport(diffReportFixture(), fixture.password)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fixture.name+".html"), []byte(html), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root := &Node{Name: "private-root", Path: "/private-root", Type: "dir"}
	normal, err := renderReport(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "normal.html"), []byte(normal), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "-", dir)
	cmd.Stdin = strings.NewReader(diffReportThemeBrowserCheck)
	output, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 77 {
		t.Skip(strings.TrimSpace(string(output)))
	}
	if err != nil {
		t.Fatalf("theme browser verification: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}

const diffReportThemeBrowserCheck = `
import sys,pathlib,glob,os,json
try:
 from playwright.sync_api import sync_playwright
except ImportError:
 print('Playwright unavailable; theme browser verification skipped');sys.exit(77)
root=pathlib.Path(sys.argv[1]);out=os.environ.get('DIFF_REPORT_THEME_SCREENSHOTS')
if out:pathlib.Path(out).mkdir(parents=True,exist_ok=True)
with sync_playwright() as p:
 binaries=[p.chromium.executable_path]+glob.glob(os.path.expanduser('~/.cache/ms-playwright/chromium-*/chrome-linux*/chrome'))
 binary=next((x for x in binaries if os.path.isfile(x)),None)
 if not binary:print('Chromium unavailable; theme browser verification skipped');sys.exit(77)
 browser=p.chromium.launch(executable_path=binary,headless=True,args=['--no-sandbox'])
 context=browser.new_context(viewport={'width':1440,'height':1000})
 page=context.new_page();errors=[];network=[]
 page.on('pageerror',lambda e:errors.append(str(e)))
 page.on('request',lambda r:network.append(r.url) if r.url.startswith(('https://','http://')) else None)
 context.add_init_script("""window.themeChanges=[];new MutationObserver(records=>{for(const r of records)if(r.attributeName==='data-theme')window.themeChanges.push(r.target.dataset.theme)}).observe(document,{subtree:true,attributes:true,attributeFilter:['data-theme']})""")
 page.goto((root/'plain.html').as_uri());page.locator('#report').wait_for(state='visible')
 assert page.locator('html').get_attribute('data-theme')=='dark'
 toggle=page.get_by_role('switch',name='Switch to light theme',exact=True)
 assert toggle.get_attribute('aria-checked')=='false' and not toggle.is_checked()
 # Compare computed surface/control colors and exact theme-icon shape to the
 # current working tree's ordinary optimized report, not an old fixture.
 reference=context.new_page();reference.goto((root/'normal.html').as_uri());reference.locator('#themeToggle').wait_for(state='attached')
 for theme in ['dark','light']:
  page.evaluate('(theme)=>applyTheme(theme,true)',theme)
  page.evaluate('window.scrollTo(0,0)')
  reference.evaluate("(theme)=>document.documentElement.dataset.theme=theme",theme)
  # The shared switcher animates its surface for 180ms. Compare settled
  # appearances rather than nondeterministic intermediate animation frames.
  page.wait_for_timeout(250);reference.wait_for_timeout(250)
  tokens=['--bg','--panel','--panel-2','--control','--line','--ink','--muted','--accent','--row-hover','--header']
  values=lambda tab:tab.evaluate('(tokens)=>Object.fromEntries(tokens.map(k=>[k,getComputedStyle(document.documentElement).getPropertyValue(k).trim()]))',tokens)
  assert values(page)==values(reference),(values(page),values(reference))
  actual=page.locator('.theme-switcher-v2').evaluate('(e)=>({width:getComputedStyle(e).width,height:getComputedStyle(e).height,radius:getComputedStyle(e).borderRadius,background:getComputedStyle(e).backgroundColor})')
  expected=reference.locator('.theme-switcher-v2').evaluate('(e)=>({width:getComputedStyle(e).width,height:getComputedStyle(e).height,radius:getComputedStyle(e).borderRadius,background:getComputedStyle(e).backgroundColor})')
  assert actual==expected,(actual,expected)
  assert page.locator('.theme-switcher-v2').inner_html()==reference.locator('.theme-switcher-v2').inner_html()
  assert page.locator('body').evaluate('(e)=>getComputedStyle(e).backgroundColor')==reference.locator('body').evaluate('(e)=>getComputedStyle(e).backgroundColor')
  if out:page.screenshot(path=str(pathlib.Path(out)/('desktop-'+theme+'.png')))
  assert page.locator('#rows tr').first.bounding_box()['height']<=40
  summary=page.locator('#summarySection').bounding_box();ranking=page.locator('#rankingsSection').bounding_box()
  assert summary['y']+summary['height']<=ranking['y']
  assert abs(summary['x']-ranking['x'])<1 and abs(summary['width']-ranking['width'])<1
  assert page.locator('#rows tr').count()==50
  # Exercise global status/search/navigation, pagination, and export in both themes.
  page.locator('#search').fill('grow.bin');page.wait_for_timeout(250)
  assert page.locator('#rows tr').count()==1
  page.locator('#search').fill('');page.locator('#status').select_option('removed');page.wait_for_timeout(250)
  assert page.locator('#rows tr').count()==1
  with page.expect_download() as dl:page.locator('#csv').click()
  assert pathlib.Path(dl.value.path()).read_text(encoding='utf-8-sig').count('\n')==263
  page.locator('#reset').click()
  if page.locator('th[data-key="delta"]').get_attribute('aria-sort')=='descending':page.get_by_role('button',name='Change',exact=True).click()
  page.get_by_role('button',name='Open directory /private-root/offset',exact=True).click()
  assert page.locator('#rows tr').count()==2
  page.locator('#reset').click();page.locator('#next').click();assert page.locator('#pageLabel').inner_text()=='2 / 6'
  page.locator('#reset').click()
  page.set_viewport_size({'width':1440,'height':1000});page.evaluate('window.scrollTo(0,0)')
  if out:page.screenshot(path=str(pathlib.Path(out)/('desktop-'+theme+'.png')))
  page.set_viewport_size({'width':390,'height':844})
  assert page.evaluate('document.documentElement.scrollWidth<=window.innerWidth')
  if out:page.screenshot(path=str(pathlib.Path(out)/('mobile-'+theme+'.png')))
  page.locator('#explorerTitle').evaluate('(e)=>window.scrollTo(0,e.getBoundingClientRect().top+window.scrollY-8)')
  assert page.locator('.scroll').evaluate('(e)=>e.scrollWidth>e.clientWidth')
  assert page.locator('#status').bounding_box()['height']>=36
  if out:page.screenshot(path=str(pathlib.Path(out)/('mobile-table-'+theme+'.png')))
  page.set_viewport_size({'width':1440,'height':1000})
 # Keyboard toggles state and persists the same key used by the normal report.
 page.evaluate("applyTheme('dark',true)");page.locator('#themeToggle').focus();page.keyboard.press('Space')
 assert page.locator('#themeToggle').is_checked()
 assert page.locator('#themeToggle').get_attribute('aria-label')=='Switch to dark theme'
 assert page.evaluate("localStorage.getItem('webdiskstat-theme')")=='light'
 page.reload();page.locator('#report').wait_for(state='visible')
 assert page.locator('html').get_attribute('data-theme')=='light'
 assert page.locator('#themeToggle').get_attribute('aria-checked')=='true'
 assert page.evaluate('window.themeChanges.every(theme=>theme==="light")'),page.evaluate('window.themeChanges')
 # Pre-unlock password dialog follows saved theme; cancellation and retry keep it.
 for theme in ['dark','light']:
  page.evaluate('(theme)=>applyTheme(theme,true)',theme)
  page.goto((root/'encrypted.html').as_uri());page.locator('#passwordDialog').wait_for(state='visible')
  assert page.locator('html').get_attribute('data-theme')==theme
  assert page.locator('#report').is_hidden()
  assert page.locator('#passwordDialog').evaluate('(e)=>getComputedStyle(e).backgroundColor')==page.locator('body').evaluate('(e)=>getComputedStyle(e).backgroundColor')
  for device,viewport in [('desktop',{'width':1440,'height':1000}),('mobile',{'width':390,'height':844})]:
   page.set_viewport_size(viewport)
   if out:page.screenshot(path=str(pathlib.Path(out)/('password-'+device+'-'+theme+'.png')))
  page.get_by_role('button',name='Cancel',exact=True).click();page.locator('#retry').wait_for(state='visible');page.locator('#retry').click()
  page.locator('#password').fill('theme-password');page.get_by_role('button',name='Unlock',exact=True).click()
  page.locator('#report').wait_for(state='visible',timeout=60000)
  assert page.locator('html').get_attribute('data-theme')==theme
  page.set_viewport_size({'width':1440,'height':1000})
 # Simulate browsers/privacy settings that deny both storage reads and writes.
 blocked=browser.new_context();blocked.add_init_script("Object.defineProperty(window,'localStorage',{get(){throw new DOMException('Storage blocked','SecurityError')}})")
 blockedPage=blocked.new_page();blockedPage.on('pageerror',lambda e:errors.append(str(e)))
 blockedPage.goto((root/'plain.html').as_uri());blockedPage.locator('#report').wait_for(state='visible')
 assert blockedPage.locator('html').get_attribute('data-theme')=='dark'
 blockedPage.locator('#themeToggle').focus();blockedPage.keyboard.press('Space')
 assert blockedPage.locator('html').get_attribute('data-theme')=='light'
 blockedPage.reload();blockedPage.locator('#report').wait_for(state='visible')
 assert blockedPage.locator('html').get_attribute('data-theme')=='dark'
 assert not errors,errors
 assert not network,network
 browser.close()
 print('PASS: normal-report palette/control parity, dark default, keyboard toggle/state, shared-key persistence/reload, pre-paint restoration, blocked storage fallback, compact desktop rows, mobile overflow/targets, search/filter/navigation/paging/CSV in both themes, pre-unlock encrypted dialogs in both themes, zero network requests')
`
