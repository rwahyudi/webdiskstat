package main

import (
	"strings"
	"testing"
)

func TestDiffReportSharesNormalLoadingAnimation(t *testing.T) {
	markup, css, err := diffLoadingIntegration()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(markup, `id="loadingOverlay"`) || !strings.Contains(markup, `id="loadingTasks"`) || !strings.Contains(css, ".loading-spinner") {
		t.Fatal("shared loading overlay is missing animation, task list, or reduced-motion styling")
	}
	html, err := renderDiffReport(diffReportFixture(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"Opening comparison", "loadingProgressFill", "Decode comparison data", "Decrypt comparison data", "Decompress comparison data", "Build directory index", "Render comparison", "await loadingFrame()", "setLoadingStep('index')", "hideLoadingOverlay()"} {
		if !strings.Contains(html, required) {
			t.Errorf("diff report missing loading integration %q", required)
		}
	}
	if strings.Contains(html, "Loading report data") || strings.Contains(html, "Preparing disk usage view") {
		t.Fatal("diff overlay should identify comparison loading rather than a normal scan")
	}
}

func TestDiffReportLoadingBrowserParity(t *testing.T) {
	root := &Node{Name: "root", Path: "/root", Type: "dir"}
	var err error
	comparison, err := renderDiffReport(diffReportFixture(), nil)
	if err != nil {
		t.Fatal(err)
	}
	password := "loading-test-password"
	normalEncrypted, err := renderReport(root, &password)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := renderDiffReport(diffReportFixture(), &password)
	if err != nil {
		t.Fatal(err)
	}
	runDiffBrowserPython(t, diffLoadingBrowserCheck, map[string]string{"normal-encrypted": normalEncrypted, "diff": comparison, "encrypted": encrypted})
}

const diffLoadingBrowserCheck = diffBrowserPythonPrelude + `
from playwright.sync_api import expect
normal=new_page();normal.goto((root/'normal-encrypted.html').as_uri())
normal.locator('#reportPasswordDialog[open]').wait_for(timeout=15000)
diff=new_page();diff.goto((root/'diff.html').as_uri())
for page in (normal,diff):
 ids=page.locator('[id]').evaluate_all('(els)=>els.map(e=>e.id)')
 assert len(ids)==len(set(ids)), 'duplicate IDs in '+page.url
 for selector in ['.loading-overlay','.loading-panel','.loading-spinner']:
  assert page.locator(selector).count()==1, selector+' in '+page.url
normal_style=normal.locator('.loading-spinner').evaluate('(e)=>{const s=getComputedStyle(e);return [s.animationName,s.width,s.height,s.borderTopColor,s.animationDuration]}')
diff_style=diff.locator('.loading-spinner').evaluate('(e)=>{const s=getComputedStyle(e);return [s.animationName,s.width,s.height,s.borderTopColor,s.animationDuration]}')
assert normal_style==diff_style,(normal_style,diff_style)
assert normal_style[0]!='none',normal_style
assert diff.locator('style').evaluate_all('(els)=>els.some(e=>e.textContent.includes("@keyframes loading-spin"))')
diff.emulate_media(reduced_motion='reduce');normal.emulate_media(reduced_motion='reduce')
assert diff.locator('.loading-spinner').evaluate('(e)=>getComputedStyle(e).animationDuration')==normal.locator('.loading-spinner').evaluate('(e)=>getComputedStyle(e).animationDuration')
encrypted=new_page();encrypted.add_init_script("""window.__loaderPaints=[];new MutationObserver(records=>{for(const r of records){if(r.target.matches&&r.target.matches('.loading-task.active'))window.__loaderPaints.push([r.target.dataset.step,!document.getElementById('loadingOverlay')?.hidden])}}).observe(document,{subtree:true,attributes:true,attributeFilter:['class']})""");encrypted.goto((root/'encrypted.html').as_uri())
encrypted.locator('#passwordDialog[open]').wait_for(timeout=15000)
assert encrypted.locator('#loadingOverlay').is_visible()
assert encrypted.locator('.loading-spinner').evaluate('(e)=>getComputedStyle(e).animationName')!='none'
encrypted.locator('#password').fill('loading-test-password');encrypted.locator('#passwordForm button.primary').click()
encrypted.locator('#diffBrowser').wait_for(state='visible',timeout=15000)
assert encrypted.locator('#loadingOverlay').evaluate('(e)=>e.hidden')
painted=encrypted.evaluate('window.__loaderPaints')
assert any(step=='index' and visible for step,visible in painted),painted
assert any(step=='render' and visible for step,visible in painted),painted
broken=new_page();broken.add_init_script("window.DecompressionStream=function(){throw new Error('bad gzip fixture')}")
broken.goto((root/'diff.html').as_uri());broken.locator('#retry').wait_for(state='visible',timeout=15000)
assert not broken.locator('#loadingOverlay').is_visible()
assert 'bad gzip fixture' in broken.locator('#loading').inner_text()
assert broken.locator('#retry').is_visible()
finish('normal/diff loader styles, motion, encrypted boot, and retry verified')
`
