package main

import "testing"

func TestDiffReportSummaryDisclosureAndCaptureDates(t *testing.T) {
	root := &DiffNode{Name: "disk", Path: "/data", Type: "dir", Status: "grown", Before: 1024, After: 2048, Delta: 1024}
	report := &DiffReport{
		Before: DiffSnapshot{Source: "baseline.json", Root: "/data", Size: 1024, Files: 2, Directories: 1, CapturedAt: "1793404800"},
		After:  DiffSnapshot{Source: "current.json", Root: "/data", Size: 2048, Files: 3, Directories: 1, CapturedAt: "2026-10-31T00:00:00Z"},
		Root: root, Increased: 1024, StatusCounts: map[string]int{"grown": 1},
	}
	html, err := renderDiffReport(report, nil)
	if err != nil {
		t.Fatal(err)
	}
	invalid := *report
	invalid.Before.CapturedAt = "not-a-date"
	invalidHTML, err := renderDiffReport(&invalid, nil)
	if err != nil {
		t.Fatal(err)
	}
	missing := *report
	missing.Before.CapturedAt = ""
	missingHTML, err := renderDiffReport(&missing, nil)
	if err != nil {
		t.Fatal(err)
	}
	runDiffBrowserPython(t, diffSummaryCheck, map[string]string{"summary": html, "invalid": invalidHTML, "missing": missingHTML})
}

const diffSummaryCheck = diffBrowserPythonPrelude + `
page=new_page();load(page,'summary')
toggle=page.locator('#summaryToggle');panel=page.locator('#summaryPanel')
assert toggle.get_attribute('aria-expanded')=='true' and panel.is_visible()
assert page.locator('#beforeDate').inner_text()==page.locator('#afterDate').inner_text(),(page.locator('#beforeDate').inner_text(),page.locator('#afterDate').inner_text())
assert '2026' in page.locator('#beforeDate').inner_text(),page.locator('#beforeDate').inner_text()
assert page.locator('#beforeDate').evaluate('(e)=>parseFloat(getComputedStyle(e).fontSize)')>=14
page.locator('.summary-details summary').click()
for selector,expected in [('#beforeSource','baseline.json'),('#afterSource','current.json')]:
 assert expected in page.locator(selector).inner_text() and '/data' in page.locator(selector).inner_text()
 assert 'Captured:' not in page.locator(selector).inner_text()
assert page.locator('#summaryNet').text_content()=='Net +1 KiB',page.locator('#summaryNet').text_content()
assert page.locator('#summarySection .summary article').first.locator('#netSize').count()==1
assert page.locator('#statusButtons button').count()==6
assert page.locator('#statusButtons button').nth(0).inner_text()=='All changes · 1'
assert page.locator('#increaseBar').evaluate('(e)=>e.style.width')=='100%'
assert page.locator('#decreaseBar').evaluate('(e)=>e.style.width')=='0%'
assert page.locator('#decreased').inner_text()=='0 B'
page.evaluate('data.increased=1;data.decreased=1024;summary()')
page.wait_for_timeout(20)
assert abs(page.locator('#increaseBar').evaluate('(e)=>parseFloat(e.style.width)')-100/1024)<0.00001
assert page.locator('#decreaseBar').evaluate('(e)=>e.style.width')=='100%'
page.evaluate('data.increased=0;data.decreased=0;summary()');page.wait_for_timeout(20)
assert page.locator('#increaseBar').evaluate('(e)=>e.style.width')=='0%'
assert page.locator('#decreaseBar').evaluate('(e)=>e.style.width')=='0%'
assert page.locator('.movement').get_attribute('role') is None
toggle.focus();page.keyboard.press('Enter');assert toggle.get_attribute('aria-expanded')=='false'
assert 'changed paths' in page.locator('#summaryContext').inner_text()
assert page.locator('#summarySection').evaluate('(e)=>e.classList.contains("is-collapsed")')
assert page.locator('#summaryPanel').get_attribute('aria-hidden')=='true' and page.locator('#summaryPanel').evaluate('(e)=>e.inert')
assert toggle.get_attribute('aria-label')=='Expand comparison summary'
page.wait_for_timeout(240);assert panel.evaluate('(e)=>e.getBoundingClientRect().height')==0
page.keyboard.press('Space');assert toggle.get_attribute('aria-expanded')=='true'
assert page.locator('#summaryPanel').get_attribute('aria-hidden')=='false' and not page.locator('#summaryPanel').evaluate('(e)=>e.inert')
assert toggle.get_attribute('aria-label')=='Collapse comparison summary'
page.keyboard.press('Enter');page.keyboard.press('Enter');assert toggle.get_attribute('aria-expanded')=='true'
page.emulate_media(reduced_motion='reduce')
toggle.click();assert page.locator('#summaryPanel').evaluate('(e)=>getComputedStyle(e).transitionDuration.split(",").every(x=>parseFloat(x)==0)')
toggle.click()
out=os.environ.get('DIFF_SUMMARY_SCREENSHOTS')
if out:
 pathlib.Path(out).mkdir(parents=True,exist_ok=True)
 for theme in ['dark','light']:
  page.evaluate('(t)=>applyTheme(t,true)',theme)
  for device,size in [('desktop',{'width':1440,'height':1000}),('mobile',{'width':390,'height':844}),('narrow',{'width':320,'height':844})]:
   page.set_viewport_size(size);page.evaluate('window.scrollTo(0,0)')
   assert page.evaluate('document.documentElement.scrollWidth<=innerWidth')
   assert page.locator('#beforeDate').is_visible()
   for date_id in ['beforeDate','afterDate']:
    heading=page.locator('#'+date_id).locator('xpath=..')
    title=heading.locator('span').bounding_box();date=heading.locator('time').bounding_box()
    assert title['y']<date['y']+date['height'] and date['y']<title['y']+title['height'],(device,date_id,title,date)
   if out:page.screenshot(path=str(pathlib.Path(out)/(device+'-'+theme+'.png')))
page.goto((root/'invalid.html').as_uri());page.locator('#tree .tree-header').wait_for()
page.locator('.summary-details summary').click()
print('INVALID',page.locator('#beforeDate').inner_text(),page.locator('#beforeTime').inner_text(),page.locator('#beforeDate').get_attribute('datetime'))
assert page.locator('#beforeDate').inner_text()=='Date unavailable',page.locator('#beforeDate').inner_text()
assert page.locator('#beforeTime').inner_text()=='Capture time unavailable',page.locator('#beforeTime').inner_text()
assert '1970' not in page.locator('#beforeDate').inner_text(),page.locator('#beforeDate').inner_text()
page.goto((root/'missing.html').as_uri());page.locator('#tree .tree-header').wait_for()
page.locator('.summary-details summary').click()
assert page.locator('#beforeDate').inner_text()=='Date unavailable',page.locator('#beforeDate').inner_text()
assert page.locator('#beforeTime').inner_text()=='Capture time unavailable',page.locator('#beforeTime').inner_text()
finish('PASS: summary disclosure keyboard/repeat, reduced motion, date formatting, unavailable date, source/root separation, and desktop/mobile layouts in both themes')
`
