package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func diffBrowserParityFixture() (*DiffReport, *Node) {
	root := &DiffNode{Name: "parity", Path: "/parity", Type: "dir", Before: 80000000, After: 90000000, Delta: 10000000, Status: "grown", AfterFiles: 3002, AfterItems: 3003}
	folder := &DiffNode{Name: "alpha-folder", Path: "/parity/alpha-folder", Type: "dir", Before: 100, After: 100000000, Delta: 99999900, Status: "grown", AfterFiles: 2, AfterItems: 2, AfterMTime: "1735689600", Children: []*DiffNode{
		{Name: "inside2.log", Path: "/parity/alpha-folder/inside2.log", Type: "file", After: 200, Delta: 200, AfterFiles: 1, Status: "added", AfterMTime: "1735689601"},
		{Name: "inside10.log", Path: "/parity/alpha-folder/inside10.log", Type: "file", After: 100, Delta: 100, AfterFiles: 1, Status: "added"},
	}}
	root.Children = []*DiffNode{folder, {Name: "file2.bin", Path: "/parity/file2.bin", Type: "file", Before: 1, After: 2, Delta: 1, AfterFiles: 1, Status: "grown"}, {Name: "file10.bin", Path: "/parity/file10.bin", Type: "file", Before: 1, After: 10, Delta: 9, AfterFiles: 1, Status: "grown"}}
	for i := 0; i < 3000; i++ {
		name := fmt.Sprintf("record-%04d.json", i)
		root.Children = append(root.Children, &DiffNode{Name: name, Path: root.Path + "/" + name, Type: "file", Before: 1024, After: int64(i+1) * 1024, Delta: int64(i) * 1024, Status: "grown", AfterFiles: 1, AfterMTime: fmt.Sprint(1735689600 + i)})
	}
	var convert func(*DiffNode) *Node
	convert = func(n *DiffNode) *Node {
		node := &Node{Name: n.Name, Path: n.Path, Type: n.Type, Size: n.After, Files: n.AfterFiles, Items: n.AfterItems, MTime: n.AfterMTime}
		for _, child := range n.Children {
			node.Children = append(node.Children, convert(child))
		}
		return node
	}
	return &DiffReport{Before: DiffSnapshot{Source: "before.json", Root: root.Path, Size: root.Before, Files: 3000, Directories: 2}, After: DiffSnapshot{Source: "after.json", Root: root.Path, Size: root.After, Files: root.AfterFiles, Directories: 2}, Root: root, Increased: root.Delta}, convert(root)
}

func runDiffBrowserPython(t *testing.T, script string, files map[string]string) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable; browser verification skipped")
	}
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(python, "-", dir)
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 77 {
		t.Skip(strings.TrimSpace(string(output)))
	}
	if err != nil {
		t.Fatalf("browser verification: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}

func TestDiffReportBrowserParity(t *testing.T) {
	report, normalRoot := diffBrowserParityFixture()
	diff, err := renderDiffReport(report, nil)
	if err != nil {
		t.Fatal(err)
	}
	normal, err := renderReport(normalRoot, nil)
	if err != nil {
		t.Fatal(err)
	}
	runDiffBrowserPython(t, diffBrowserParityCheck, map[string]string{"diff": diff, "normal": normal})
}

func TestDiffReportBrowserGroupedChanges(t *testing.T) {
	root := &DiffNode{Name: "changes", Path: "/changes", Type: "dir", Status: "grown"}
	for _, sample := range []struct {
		name          string
		before, after int64
	}{{"increase", 10, 15}, {"decrease", 10, 5}, {"zero", 10, 10}, {"new", 0, 5}, {"empty", 0, 0}, {"removed", 10, 0}} {
		root.Children = append(root.Children, &DiffNode{Name: sample.name, Path: root.Path + "/" + sample.name, Type: "dir", Before: sample.before, After: sample.after, Delta: sample.after - sample.before, BeforeItems: int(sample.before), AfterItems: int(sample.after), Status: "grown"})
	}
	html, err := renderDiffReport(&DiffReport{Root: root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	runDiffBrowserPython(t, diffBrowserGroupedCheck, map[string]string{"groups": html})
}

func TestDiffReportIconSupport(t *testing.T) {
	const legacy = `function materialIconLookupName(node) {
  return node.name;
}
function materialIconSVG(node) {
  const icon = MaterialFileIcons.getIcon(materialIconLookupName(node));
  return icon && icon.svg ? icon.svg : "";
}
function makeFileIcon(node) {
  const icon = document.createElement("span");
  icon.innerHTML = materialIconSVG(node);
  return icon;
}
`
	declarations, err := normalBrowserIconSupport(legacy)
	if err != nil || declarations != "" {
		t.Fatalf("legacy icon functions must not acquire modern dependencies: %q, %v", declarations, err)
	}
	modern := strings.Replace(legacy, `return icon && icon.svg ? icon.svg : "";`, `return icon && icon.svg ? icon.svg : GENERIC_FILE_ICON;`, 1)
	if _, err := normalBrowserIconSupport(modern); err == nil {
		t.Fatal("a referenced fallback must not be silently invented")
	}
	modern = "const GENERIC_FILE_ICON = \"<svg></svg>\";\n" + modern
	declarations, err = normalBrowserIconSupport(modern)
	if err != nil || !strings.Contains(declarations, "const GENERIC_FILE_ICON") {
		t.Fatalf("modern renderer fallback was not extracted: %q, %v", declarations, err)
	}
	for _, name := range []string{"MIME_ICON_FILENAMES", "MIME_FAMILY_ICON_FILENAMES"} {
		withMap := strings.Replace(modern, `return node.name;`, `return `+name+`.get(node.name) || node.name;`, 1)
		if _, err := normalBrowserIconSupport(withMap); err == nil {
			t.Fatalf("referenced map %s must be present", name)
		}
		withMap = "const " + name + " = new Map([]);\n" + withMap
		declarations, err = normalBrowserIconSupport(withMap)
		if err != nil || !strings.Contains(declarations, "const "+name) {
			t.Fatalf("map dependency was not extracted: %q, %v", declarations, err)
		}
	}
}

const diffBrowserPythonPrelude = `
import sys,pathlib,glob,os,json,csv,io
try:
 from playwright.sync_api import sync_playwright
except ImportError:
 print('Playwright unavailable; browser verification skipped');sys.exit(77)
root=pathlib.Path(sys.argv[1])
p=sync_playwright().start()
binary=next((s for s in [p.chromium.executable_path]+glob.glob(os.path.expanduser('~/.cache/ms-playwright/chromium-*/chrome-linux*/chrome')) if os.path.isfile(s)),None)
if not binary:print('Chromium unavailable; browser verification skipped');sys.exit(77)
browser=p.chromium.launch(executable_path=binary,headless=True,args=['--no-sandbox'])
context=browser.new_context(viewport={'width':1440,'height':1100},accept_downloads=True)
errors=[];network=[]
def new_page():
 page=context.new_page();page.on('pageerror',lambda e:errors.append(str(e)));page.on('request',lambda r:network.append(r.url) if r.url.startswith(('http://','https://')) else None);return page
def load(page,name):
 page.goto((root/(name+'.html')).as_uri())
 try:page.locator('#tree .tree-header').wait_for(state='visible',timeout=15000)
 except Exception:raise AssertionError('Report did not boot: '+str(errors)+' '+page.locator('body').inner_text()[:1500])
def finish(message):
 assert not errors,errors
 assert not network,network
 browser.close();p.stop();print(message)
`

const diffBrowserGroupedCheck = diffBrowserPythonPrelude + `
page=new_page();load(page,'groups')
sizeColumns=['before','size','delta','percent'];items=['beforeItems','items','itemsDelta','itemsPercent']
assert page.locator('.tree-column-group').all_text_contents()==['Size','Items']
def aligned():
 for label,keys in [('Size',sizeColumns),('Items',items)]:
  visible=[page.locator('.tree-header-cell[data-column="'+key+'"]') for key in keys if page.locator('.tree-header-cell[data-column="'+key+'"]').count()]
  group=page.locator('.tree-column-group').filter(has_text=label)
  if not visible:assert group.count()==0;continue
  box=group.bounding_box();first=visible[0].bounding_box();last=visible[-1].bounding_box()
  assert abs(box['x']-first['x'])<1 and abs(box['x']+box['width']-last['x']-last['width'])<1
aligned()
for theme in ['dark','light']:
 page.evaluate('(t)=>applyTheme(t,true)',theme);page.wait_for_timeout(400)
 for name,cls,percent in [('increase','up','+50%'),('decrease','down','-50%'),('zero','muted','0%'),('new','up','New'),('empty','muted','0%'),('removed','down','-100%')]:
  row=page.locator('#tree .row[title="/changes/'+name+'"]')
  for key in ['delta','percent','itemsDelta','itemsPercent']:
   cell=row.locator('[data-column="'+key+'"]');assert cls in cell.get_attribute('class')
   if key in ['percent','itemsPercent']:assert cell.inner_text()==percent
   if cls!='muted':
    actual=cell.evaluate('(e)=>getComputedStyle(e).color')
    expected=page.evaluate('(cls)=>{const e=document.createElement("span");e.style.color="var(--"+cls+")";document.body.append(e);const color=getComputedStyle(e).color;e.remove();return color}',cls)
    assert actual==expected,(theme,name,key,actual,expected)
    channels=[int(c.strip()) for c in actual.removeprefix('rgb(').removesuffix(')').split(',')]
    assert (channels[0]>channels[1] if cls=='up' else channels[1]>channels[0]),(theme,name,key,actual)
# Each nested label sorts independently with a metric-specific accessible name.
for key in sizeColumns+items:
 header=page.locator('.tree-header-cell[data-column="'+key+'"]')
 button=header.locator('.tree-sort');assert ('Size ' if key in sizeColumns else 'Items ') in button.get_attribute('aria-label')
 button.focus();button.press('Enter')
 assert page.evaluate('DiffBrowser.snapshot().sortKey')==key
 assert page.locator('.tree-header-cell[data-column="'+key+'"]').get_attribute('aria-sort') in ['ascending','descending']
handle=page.locator('.tree-header-cell[data-column="beforeItems"] .tree-column-resizer')
handle.focus();handle.press('ArrowRight');aligned()
saved=page.evaluate('JSON.parse(localStorage.getItem("webdiskstat-diff-tree-column-widths")).beforeItems')
assert saved>82
def toggle(label,checked):
 page.get_by_role('button',name='Column settings',exact=True).click()
 page.locator('.tree-columns-menu').get_by_label(label,exact=True).set_checked(checked)
 page.keyboard.press('Escape')
toggle('Size before',False);aligned()
for label in ['Items before','Items after','Items change','Items change %']:toggle(label,False)
aligned();page.reload();page.locator('.tree-header').wait_for()
assert page.locator('.tree-column-group').all_text_contents()==['Size']
assert page.locator('.tree-header-cell[data-column="before"]').count()==0
assert page.evaluate('JSON.parse(localStorage.getItem("webdiskstat-diff-tree-column-widths")).beforeItems')==saved
toggle('Size before',True)
for label in ['Items before','Items after','Items change','Items change %']:toggle(label,True)
toggle('Files before',True);toggle('Files after',True)
assert page.locator('.tree-header-cell[data-column="beforeFiles"]').count()==1
assert page.locator('.tree-header-cell[data-column="files"]').count()==1
toggle('Files before',False);toggle('Files after',False)
out=os.environ.get('DIFF_BROWSER_GROUPED_SCREENSHOTS')
if out:pathlib.Path(out).mkdir(parents=True,exist_ok=True)
for theme in ['dark','light']:
 page.evaluate('(t)=>applyTheme(t,true)',theme);page.wait_for_timeout(250)
 for device,size in [('desktop',{'width':1440,'height':1100}),('mobile',{'width':390,'height':844})]:
  page.set_viewport_size(size);page.locator('#diffBrowser').scroll_into_view_if_needed();aligned()
  assert page.evaluate('document.documentElement.scrollWidth<=innerWidth')
  header=page.locator('.tree-header').bounding_box();row=page.locator('#tree .row[data-id]').first.bounding_box()
  assert row['y']>=header['y']+header['height']-1
  if device=='mobile':
   page.locator('#tree').evaluate('(e)=>e.scrollLeft=e.scrollWidth');aligned()
   assert page.locator('#tree').evaluate('(e)=>e.scrollLeft')>0
   page.locator('#tree').evaluate('(e)=>e.scrollLeft=0')
  if out:page.screenshot(path=str(pathlib.Path(out)/(device+'-'+theme+'.png')))
finish('PASS: Size/Items groups, signed changes and percent edge cases in both themes, all nested keyboard sorts, resize/group alignment, visibility and persistence, optional file columns, narrow horizontal scrolling without page overflow')
`

const diffBrowserParityCheck = diffBrowserPythonPrelude + `
normal=new_page();diff=new_page();load(normal,'normal');load(diff,'diff');pages=[normal,diff]
def snapshot(page):
 return page.evaluate('DiffBrowser.snapshot()' if page==diff else '({path:state.current.path,selected:state.selected.path,sortKey:state.sortKey,sortDir:state.sortDir,total:currentTreeChildren().length})')
def same():
 a=snapshot(normal);b=snapshot(diff);assert a==b,(a,b)
def key_both(key):
 for page in pages:
  page.locator('#tree .row[data-id]').first.focus();page.keyboard.press(key)
 same()
for page in pages:
 # Match usable row space, accounting for the diff's grouped header and its border.
 height=432+page.locator('.tree-header').bounding_box()['height']-normal.locator('.tree-header').bounding_box()['height']
 page.locator('#tree').evaluate('(e,height)=>{e.style.flex="0 0 "+height+"px";e.style.height=height+"px";e.style.minHeight=height+"px"}',height)
 page.evaluate('renderVisibleTreeRows(true)' if page==normal else 'DiffBrowser.refresh()')
 assert 0<page.locator('#tree .row[data-id]').count()<80
 assert page.locator('#tree .tree-body').evaluate('(e)=>e.offsetHeight')>100000
same()
key_both('ArrowDown');key_both('ArrowUp');key_both('PageDown');key_both('PageUp');key_both('End');key_both('Home')
for key in ['n','n','s','C','m','M']:
 key_both(key)
 # Both reports use identical stable natural-name ties and missing-time ordering.
 first=lambda page:page.locator('#tree .row[data-id]').evaluate_all('(rows)=>rows.slice(0,12).map(r=>r.title)')
 assert first(normal)==first(diff),(key,first(normal),first(diff))
# Natural names place file2 before file10; changing sort retains selection.
for page in pages:
 page.locator('#tree .row[data-id]').first.focus();page.keyboard.press('n')
 names=page.locator('#tree .row[data-id]').evaluate_all('(rows)=>rows.map(r=>r.title)')
 assert names.index('/parity/file2.bin')<names.index('/parity/file10.bin')
 page.locator('#tree .row[title="/parity/alpha-folder"]').click()
 assert snapshot(page)['path']=='/parity'
 assert snapshot(page)['selected']=='/parity/alpha-folder'
 page.locator('#tree .row[title="/parity/alpha-folder"]').dblclick()
 assert snapshot(page)['path']=='/parity/alpha-folder'
 assert '#path=' in page.url
 page.locator('#tree .row[data-id]').first.focus();page.keyboard.press('ArrowLeft')
 assert snapshot(page)['path']=='/parity'
 assert page.get_by_role('button',name='Go up one directory',exact=True).is_disabled()
 page.locator('#tree .row[title="/parity/alpha-folder"]').focus();page.keyboard.press('Space')
 assert snapshot(page)['path']=='/parity/alpha-folder'
 page.get_by_role('button',name='Go up one directory',exact=True).click();assert snapshot(page)['path']=='/parity'
 page.go_back();assert snapshot(page)['path']=='/parity/alpha-folder'
 page.go_forward();assert snapshot(page)['path']=='/parity'
 page.goto((root/(('diff' if page==diff else 'normal')+'.html')).as_uri()+'#path=%2Fparity%2Falpha-folder')
 page.locator('#tree .row[data-id]').first.wait_for();assert snapshot(page)['path']=='/parity/alpha-folder'
 page.evaluate('Object.defineProperty(navigator,"clipboard",{configurable:true,value:{writeText:async value=>window.__copied=value}})')
 page.get_by_role('button',name='Copy current path: /parity/alpha-folder',exact=True).click();page.wait_for_timeout(50)
 assert page.evaluate('window.__copied')=='/parity/alpha-folder'
 page.get_by_role('button',name='Root',exact=True).click();assert snapshot(page)['path']=='/parity'
 # Normal suggestion combobox, not a filtered browser table.
 page.locator('#tree .row[data-id]').first.focus();page.keyboard.press('/')
 search=page.locator('#search' if page==diff else '#searchInput');assert search.evaluate('(e)=>document.activeElement===e')
 search.fill('inside2.log');page.wait_for_timeout(250)
 assert search.get_attribute('aria-expanded')=='true'
 assert page.locator('#searchResults .search-result').count()==1
 assert snapshot(page)['path']=='/parity'
 search.press('Enter');assert snapshot(page)['path']=='/parity/alpha-folder';assert snapshot(page)['selected']=='/parity/alpha-folder/inside2.log'
 # Missing timestamps remain last in both ascending and descending order.
 for shortcut in ['m','M']:
  page.locator('#tree .row[data-id]').first.focus();page.keyboard.press(shortcut)
  assert page.locator('#tree .row[data-id]').first.get_attribute('title')=='/parity/alpha-folder/inside2.log'
 page.locator('#tree .row[data-id]').first.focus();page.keyboard.press('Control+f');assert search.evaluate('(e)=>document.activeElement===e')
 sorting=(snapshot(page)['sortKey'],snapshot(page)['sortDir']);search.fill('m');search.press('n');assert search.input_value()=='mn';assert (snapshot(page)['sortKey'],snapshot(page)['sortDir'])==sorting
 search.fill('zzz no such path');page.wait_for_timeout(200);assert 'No matches' in page.locator('#searchResults').inner_text()
 search.press('Escape');assert search.get_attribute('aria-expanded')=='false'
 page.get_by_role('button',name='Column settings',exact=True).click()
 label='Modified after' if page==diff else 'Modified'
 page.locator('.tree-columns-menu').get_by_label(label,exact=True).check()
 assert page.locator('.tree-header-cell[data-column="modified"]').count()==1
 page.keyboard.press('Escape');assert page.locator('.tree-columns-menu').is_hidden()
 handle=page.locator('.tree-header-cell[data-column="modified"] .tree-column-resizer')
 before=page.locator('.tree-header-cell[data-column="modified"]').bounding_box()['width']
 handle.focus();handle.press('ArrowRight')
 after=page.locator('.tree-header-cell[data-column="modified"]').bounding_box()['width'];assert after>before
 widthkey='webdiskstat-diff-tree-column-widths' if page==diff else 'webdiskstat-tree-column-widths'
 saved=page.evaluate('(key)=>JSON.parse(localStorage.getItem(key)).modified',widthkey);assert saved>0
 # Pointer resizing is the same shared implementation as keyboard resizing.
 namehandle=page.locator('.tree-header-cell[data-column="name"] .tree-column-resizer');bounds=namehandle.bounding_box()
 page.mouse.move(bounds['x']+bounds['width']/2,bounds['y']+bounds['height']/2);page.mouse.down();page.mouse.move(bounds['x']+bounds['width']/2+40,bounds['y']+bounds['height']/2,steps=4);page.mouse.up()
 assert page.evaluate('(key)=>JSON.parse(localStorage.getItem(key)).name',widthkey)>=300
 page.reload();page.locator('.tree-header-cell[data-column="modified"]').wait_for();assert page.evaluate('(key)=>JSON.parse(localStorage.getItem(key)).modified',widthkey)==saved
 page.locator('#tree .row[data-id]').first.focus();page.keyboard.press('?')
 help=page.locator('#browserHelp' if page==diff else '#helpPage');assert help.is_visible();page.keyboard.press('Escape');assert help.is_hidden()
 if page==diff:
  page.locator('#browserHelpButton').click();assert help.is_visible();page.keyboard.press('Escape');assert help.is_hidden()
# Diff-specific columns/filters and the normal browser interaction remain distinct.
diff.locator('#reset').click();diff.locator('#status').select_option('added')
assert snapshot(diff)['total']==1
diff.locator('#tree .row[title="/parity/alpha-folder"]').dblclick();assert snapshot(diff)['total']==2
diff.locator('#reset').click()
assert diff.locator('#pageSize,#previous,#next').count()==0
assert diff.locator('.tree-header-cell[data-column="before"]').count()==1
assert diff.locator('.tree-header-cell[data-column="delta"]').count()==1
assert diff.locator('.tree-header-cell[data-column="percent"]').count()==1
out=os.environ.get('DIFF_BROWSER_PARITY_SCREENSHOTS')
if out:pathlib.Path(out).mkdir(parents=True,exist_ok=True)
for theme in ['dark','light']:
 diff.evaluate('(theme)=>applyTheme(theme,true)',theme);diff.wait_for_timeout(250)
 for device,size in [('desktop',{'width':1440,'height':1100}),('mobile',{'width':390,'height':844})]:
  diff.set_viewport_size(size);diff.evaluate('window.scrollTo(0,0)')
  if out:diff.screenshot(path=str(pathlib.Path(out)/('summary-'+device+'-'+theme+'.png')))
  diff.locator('#diffBrowser').evaluate('(e)=>window.scrollTo(0,e.getBoundingClientRect().top+window.scrollY)')
  assert diff.evaluate('document.documentElement.scrollWidth<=window.innerWidth')
  summary_columns=diff.locator('#summarySection .summary').evaluate('(e)=>getComputedStyle(e).gridTemplateColumns.split(" ").length')
  assert summary_columns==(4 if device=='desktop' else 2),summary_columns
  assert diff.locator('#diffBrowser > .section-head').count()==0
  gross_index=diff.evaluate("Array.from(document.querySelectorAll('#summarySection .summary article')).indexOf(document.querySelector('#increased').closest('article'))")
  net_index=diff.evaluate("Array.from(document.querySelectorAll('#summarySection .summary article')).indexOf(document.querySelector('#netSize').closest('article'))")
  assert gross_index==2 and net_index==3,(gross_index,net_index)
  assert diff.locator('#browserHelpButton').evaluate('(e)=>e.closest("header")!==null')
  header=diff.locator('header').bounding_box();help_box=diff.locator('#browserHelpButton').bounding_box()
  assert abs(help_box['y']+help_box['height']/2-(header['y']+header['height']/2))<3
  diff.locator('#browserHelpButton').click()
  help_dialog=diff.locator('#browserHelp');assert help_dialog.is_visible()
  columns=help_dialog.locator('.browser-help-grid').evaluate('(e)=>getComputedStyle(e).gridTemplateColumns.split(" ").length')
  assert columns==(3 if device=='desktop' else 1),columns
  assert help_dialog.bounding_box()['width']>800 if device=='desktop' else help_dialog.bounding_box()['width']>=350
  assert help_dialog.evaluate('(e)=>getComputedStyle(e).scrollbarWidth')=='none'
  assert help_dialog.evaluate('(e)=>e.scrollHeight<=e.clientHeight')
  if out:diff.screenshot(path=str(pathlib.Path(out)/('help-'+device+'-'+theme+'.png')))
  diff.keyboard.press('Escape');assert help_dialog.is_hidden()
  assert diff.locator('#tree .row[data-id]').count()<80
  assert diff.locator('#status').evaluate('(e)=>e.closest(".browser-toolbar")!==null')
  if device=='desktop':
   search_y=diff.locator('#search').bounding_box()['y']
   assert all(abs(diff.locator(selector).bounding_box()['y']-search_y)<3 for selector in ['#status','#reset','#csv'])
  if device=='mobile':
   assert diff.locator('#status').bounding_box()['width']>=120
   diff.locator('#tree').focus();diff.keyboard.press('ArrowRight');assert diff.locator('#tree').evaluate('(e)=>e.scrollLeft')>0
   diff.keyboard.press('ArrowLeft');diff.locator('#tree').evaluate('(e)=>e.scrollLeft=0')
  if out:diff.screenshot(path=str(pathlib.Path(out)/(device+'-'+theme+'.png')))
finish('PASS: same-scenario normal/diff selection, navigation, Space/double click, arrows/pages/home/end, natural sort and n/s/C/m/M shortcuts, suggestion search, URL/history/bookmark, copy path, columns/persisted resize, help, virtualized 3003-row DOM, status context, both themes/mobile offline')
`

const diffBrowserRegressionCheck = diffBrowserPythonPrelude + `
page=new_page();load(page,'plain')
assert 0<page.locator('#tree .row[data-id]').count()<80
assert page.evaluate('DiffBrowser.snapshot().total')==262
assert page.locator('#netSize').inner_text()=='0 B'
assert page.locator('#reportSecurityInfo').is_visible()
page.locator('#reportSecurityInfo summary').click();assert page.locator('#reportSecurityExplanation').is_visible()
page.get_by_role('button',name='Close encryption information').click();assert not page.locator('#reportSecurityInfo').evaluate('(e)=>e.open')
assert page.evaluate('window.diffXSS') is None
assert 'baseline-secret.json' in page.locator('#beforeSource').inner_text()
times=page.evaluate("[captureTime('1793404800'),captureTime('2026-10-31T00:00:00Z'),captureTime(''),captureTime('invalid'),captureTime(null),captureTime('99999999999999999')]")
assert times[0]==times[1] and '2026' in times[0] and all(t=='Capture time unavailable' for t in times[2:]),times
assert page.evaluate('typeof largestChanges')=='undefined'
assert page.locator('[data-category="growingFiles"] tbody tr').count()==10
page.locator('#tree').evaluate('(e)=>e.scrollTop=e.scrollHeight');page.wait_for_timeout(80)
assert 0<page.locator('#tree .row[data-id]').count()<80
page.locator('#search').fill('grow.bin');page.wait_for_timeout(200)
assert page.locator('#searchResults .search-result').count()==1
assert page.evaluate('DiffBrowser.snapshot().path')=='/private-root'
page.locator('#search').press('Enter')
assert page.evaluate('DiffBrowser.snapshot().selected')=='/private-root/offset/grow.bin'
assert page.locator('#tree .row[data-id]').count()==2
page.locator('#reset').click();page.locator('#status').select_option('removed')
assert page.evaluate('DiffBrowser.snapshot().total')==1
assert 'offset' in page.locator('#tree .row[data-id]').inner_text()
page.locator('#tree .row[data-id]').dblclick()
assert page.locator('#tree .row[data-id]').count()==1 and 'shrink.bin' in page.locator('#tree').inner_text()
page.locator('#reset').click();page.locator('#search').fill('no-such-file');page.wait_for_timeout(200)
assert 'No matches' in page.locator('#searchResults').inner_text()
assert page.evaluate('DiffBrowser.snapshot().total')==262
page.locator('#reset').click()
with page.expect_download() as dl:page.locator('#csv').click()
records=list(csv.DictReader(io.StringIO(pathlib.Path(dl.value.path()).read_text(encoding='utf-8-sig'))))
assert len(records)==263 and all(r['status']!='unchanged' for r in records)
assert not any(r['path']=='/private-root/offset' for r in records)
safe=page.evaluate("['=SUM(A1)', '+cmd', '-cmd', '@cmd', '  =cmd', '\\tcmd', '\\rcmd', 'normal', 'a\\\"b'].map(csvCell)")
assert all(x.startswith('"\'') for x in safe[:7]),safe
assert safe[7]=='"normal"' and safe[8]=='"a""b"'
page.locator('#search').fill('grow.bin');page.wait_for_timeout(200)
with page.expect_download() as dl:page.locator('#csv').click()
assert len(list(csv.DictReader(io.StringIO(pathlib.Path(dl.value.path()).read_text(encoding='utf-8-sig')))))==263
page.locator('#reset').click();page.locator('.tree-header-cell[data-column="name"] .tree-sort').click()
assert page.locator('.tree-header-cell[data-column="name"]').get_attribute('aria-sort')=='ascending'
assert '</script>' in page.locator('#tree').inner_text() and page.evaluate('window.diffXSS') is None
page.locator('#search').focus();page.keyboard.press('Tab');assert page.locator('#status').evaluate('(e)=>document.activeElement===e')
for selector in ['#reset','#csv']:
 page.keyboard.press('Tab');assert page.locator(selector).evaluate('(e)=>document.activeElement===e')
page.locator('#browserHelpButton').focus();assert page.locator('#browserHelpButton').evaluate('(e)=>document.activeElement===e')
page.set_viewport_size({'width':390,'height':844});assert page.evaluate('document.documentElement.scrollWidth<=window.innerWidth')
load(page,'empty');assert 'No changes found' in page.locator('#empty').inner_text();assert page.locator('#csv').is_disabled()
page.goto((root/'encrypted.html').as_uri());page.locator('#passwordDialog').wait_for(state='visible')
page.locator('#password').fill('wrong');page.get_by_role('button',name='Unlock',exact=True).click();page.get_by_text('Incorrect password. Try again:',exact=True).wait_for()
page.locator('#password').fill('browser-password');page.get_by_role('button',name='Unlock',exact=True).click();page.locator('#report').wait_for(state='visible',timeout=60000)
assert 'baseline-secret.json' in page.locator('#beforeSource').inner_text()
fallbackContext=browser.new_context();fallbackContext.add_init_script('Object.defineProperty(globalThis,"crypto",{value:undefined,configurable:true})')
fallback=fallbackContext.new_page();fallback.on('pageerror',lambda e:errors.append(str(e)))
fallback.goto((root/'encrypted.html').as_uri());fallback.locator('#passwordDialog').wait_for(state='visible')
fallback.locator('#password').fill('browser-password');fallback.get_by_role('button',name='Unlock',exact=True).click();fallback.locator('#report').wait_for(state='visible',timeout=120000)
assert 0<fallback.locator('#tree .row[data-id]').count()<80
finish('PASS: capture times, default-ten dedicated rankings, bounded virtual scrolling, suggestion search, status ancestors/navigation, CSV completeness/formula protection, no-change, XSS, keyboard, encrypted retry/unlock and pure-JS KDF fallback, offline')
`

const diffBrowserThemeCheck = diffBrowserPythonPrelude + `
page=new_page();reference=new_page();load(page,'plain');load(reference,'normal')
assert page.locator('html').get_attribute('data-theme')=='dark'
assert page.locator('#themeToggle').get_attribute('aria-checked')=='false'
for theme in ['dark','light']:
 page.evaluate('(t)=>applyTheme(t,true)',theme);reference.evaluate('(t)=>document.documentElement.dataset.theme=t',theme)
 page.wait_for_timeout(400);reference.wait_for_timeout(400)
 tokens=['--bg','--panel','--panel-2','--control','--line','--ink','--muted','--accent','--row-hover','--header']
 values=lambda tab:tab.evaluate('(tokens)=>Object.fromEntries(tokens.map(k=>[k,getComputedStyle(document.documentElement).getPropertyValue(k).trim()]))',tokens)
 assert values(page)==values(reference)
 shape=lambda tab:tab.locator('.theme-switcher-v2').evaluate('(e)=>({width:getComputedStyle(e).width,height:getComputedStyle(e).height,radius:getComputedStyle(e).borderRadius,background:getComputedStyle(e).backgroundColor})')
 assert shape(page)==shape(reference),(shape(page),shape(reference))
 assert page.locator('.theme-switcher-v2').inner_html()==reference.locator('.theme-switcher-v2').inner_html()
 summary=page.locator('#summarySection').bounding_box();browser_box=page.locator('#diffBrowser').bounding_box();ranking=page.locator('#rankingsSection').bounding_box()
 assert summary['y']+summary['height']<=browser_box['y'] and browser_box['y']+browser_box['height']<=ranking['y']
 assert abs(summary['x']-browser_box['x'])<1 and abs(browser_box['x']-ranking['x'])<1
 assert abs(summary['width']-browser_box['width'])<1 and abs(browser_box['width']-ranking['width'])<1
 assert page.locator('#tree .row[data-id]').first.bounding_box()['height']==36
page.evaluate("applyTheme('dark',true)");page.locator('#themeToggle').focus();page.keyboard.press('Space')
assert page.locator('#themeToggle').is_checked() and page.locator('#themeToggle').get_attribute('aria-label')=='Switch to dark theme'
assert page.evaluate("localStorage.getItem('webdiskstat-theme')")=='light'
context.add_init_script("window.themeChanges=[];new MutationObserver(records=>{for(const r of records)window.themeChanges.push(r.target.dataset.theme)}).observe(document,{subtree:true,attributes:true,attributeFilter:['data-theme']})")
page.reload();page.locator('#tree .tree-header').wait_for()
assert page.locator('#themeToggle').get_attribute('aria-checked')=='true'
assert page.evaluate('window.themeChanges.every(t=>t==="light")')
for theme in ['dark','light']:
 page.evaluate('(t)=>applyTheme(t,true)',theme);page.goto((root/'encrypted.html').as_uri());page.locator('#passwordDialog').wait_for(state='visible')
 assert page.locator('html').get_attribute('data-theme')==theme and page.locator('#report').is_hidden()
 # Dialogs use the elevated panel surface, which need not equal the body canvas.
 dialog_bg=page.locator('#passwordDialog').evaluate('(e)=>getComputedStyle(e).backgroundColor')
 panel_bg=page.evaluate('()=>{const e=document.createElement("span");e.style.backgroundColor="var(--panel)";document.body.append(e);const color=getComputedStyle(e).backgroundColor;e.remove();return color}')
 assert dialog_bg==panel_bg,(theme,dialog_bg,panel_bg)
 page.get_by_role('button',name='Cancel',exact=True).click();page.locator('#retry').click();page.locator('#password').fill('theme-password');page.get_by_role('button',name='Unlock',exact=True).click()
 page.locator('#report').wait_for(state='visible',timeout=60000);assert page.locator('html').get_attribute('data-theme')==theme
blocked=browser.new_context();blocked.add_init_script("Object.defineProperty(window,'localStorage',{get(){throw new DOMException('Storage blocked','SecurityError')}})")
tab=blocked.new_page();tab.on('pageerror',lambda e:errors.append(str(e)));load(tab,'plain')
assert tab.locator('html').get_attribute('data-theme')=='dark'
tab.locator('#themeToggle').focus();tab.keyboard.press('Space');assert tab.locator('html').get_attribute('data-theme')=='light'
tab.get_by_role('button',name='Column settings',exact=True).click();tab.locator('.tree-columns-menu').get_by_label('Modified after',exact=True).check()
assert tab.locator('.tree-header-cell[data-column="modified"]').count()==1
tab.reload();tab.locator('#tree .tree-header').wait_for();assert tab.locator('html').get_attribute('data-theme')=='dark'
finish('PASS: authoritative normal palette/control parity, dark default, keyboard/persistence/pre-paint restoration, blocked theme/column storage, independent full-width sections, 36px rows, pre-unlock dark/light encryption and cancellation/retry, offline')
`

const diffBrowserRankingsCheck = diffBrowserPythonPrelude + `
page=new_page();load(page,'plain')
assert page.locator('h1').count()==0 and 'What changed on disk?' not in page.locator('body').inner_text()
assert page.locator('script[type="application/json"]').count()==1 and page.evaluate('typeof largestChanges')=='undefined'
categories=['growingFiles','shrinkingFiles','growingDirectories','shrinkingDirectories']
def counts():return [page.locator('[data-category="'+k+'"] tbody tr').count() for k in categories]
def geometry():
 boxes=[page.locator('#summarySection').bounding_box(),page.locator('#diffBrowser').bounding_box(),page.locator('#rankingsSection').bounding_box()]
 assert all(abs(b['x']-boxes[0]['x'])<1 and abs(b['width']-boxes[0]['width'])<1 for b in boxes)
 assert all(a['y']+a['height']<=b['y'] for a,b in zip(boxes,boxes[1:]))
 assert page.evaluate('document.documentElement.scrollWidth<=window.innerWidth')
assert page.locator('#rankLimit').input_value()=='10' and counts()==[10]*4
assert page.locator('[data-category="growingFiles"] tbody button').first.inner_text()=='grow.bin'
assert page.locator('[data-category="growingFiles"] tbody button').nth(1).inner_text()=='growingFiles-001'
for limit in ['10','25','50','100']:
 page.locator('#rankLimit').select_option(limit);assert counts()==[int(limit)]*4;geometry()
 assert page.evaluate('DiffBrowser.snapshot().total')==262
page.locator('#rankLimit').select_option('25');original=page.locator('#rankings').inner_html()
page.locator('#search').fill('grow.bin');page.wait_for_timeout(200);assert page.locator('#rankings').inner_html()==original
page.locator('#search').press('Enter');assert page.locator('#tree .row[data-id]').count()==2 and page.locator('#rankings').inner_html()==original
page.locator('#reset').click();page.locator('#status').select_option('removed');assert page.locator('#rankings').inner_html()==original
page.locator('#reset').click();page.locator('.tree-header-cell[data-column="name"] .tree-sort').click();assert page.locator('#rankings').inner_html()==original
page.locator('[data-category="growingFiles"] button').first.click();assert page.evaluate('DiffBrowser.snapshot().selected')=='/private-root/offset/grow.bin'
assert page.locator('#tree .row[data-id]').count()==2 and 'offset' in page.locator('#crumbs').inner_text()
page.locator('#reset').click();page.locator('[data-category="growingDirectories"] button').first.click();assert page.locator('#tree .row[data-id]').count()==2
page.locator('#reset').click()
for theme in ['dark','light']:
 page.evaluate('(t)=>applyTheme(t,true)',theme)
 for size in [{'width':1440,'height':1100},{'width':390,'height':844}]:
  page.set_viewport_size(size);page.locator('#rankLimit').select_option('10');geometry()
  assert page.locator('.ranking-table td:last-child').evaluate_all('(cells)=>cells.every(e=>e.scrollWidth<=e.clientWidth)')
  page.locator('#rankLimit').select_option('100');assert counts()==[100]*4;geometry()
  assert page.locator('.ranking-scroll').first.evaluate('(e)=>e.scrollHeight>e.clientHeight')
page.locator('#rankLimit').select_option('10')
page.evaluate('changed=[];visible=[];data.root.children=[];current=data.root;refresh();rankings()')
assert counts()==[10]*4 and page.locator('#tree .row[data-id]').count()==0
page.locator('[data-category="growingFiles"] button').first.click();assert page.locator('#rankingMessage').is_visible()
page.evaluate('for(const rows of Object.values(data.rankings))rows.push(...rows);rankings()');page.locator('#rankLimit').select_option('100');assert counts()==[100]*4
load(page,'empty');assert page.evaluate('DiffBrowser.snapshot().total')==262 and page.locator('#rankings button').count()==0
for limit in ['10','25','50','100']:
 page.locator('#rankLimit').select_option(limit);assert page.locator('.ranking-empty').count()==4 and page.locator('#rankings button').count()==0
page.goto((root/'encrypted.html').as_uri());page.locator('#passwordDialog').wait_for(state='visible');page.locator('#password').fill('rankings-password');page.get_by_role('button',name='Unlock',exact=True).click();page.locator('#report').wait_for(state='visible',timeout=60000)
assert counts()==[10]*4 and page.evaluate('DiffBrowser.snapshot().total')==262
page.locator('#rankLimit').select_option('100');assert counts()==[100]*4
finish('PASS: full-width summary/rankings/browser, independent embedded rankings, 10/25/50/100 cap, source order, search/filter/sort/virtualization independence, path-based file/dir navigation, empty-no-fallback, both themes/mobile, encrypted single unlock, offline')
`
