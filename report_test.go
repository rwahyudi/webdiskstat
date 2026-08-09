package main

import (
	"strings"
	"testing"
)

func TestReportIncludesMaterialFileIconSupport(t *testing.T) {
	report, err := renderReport(&Node{
		Name: "root",
		Path: "root",
		Type: "dir",
		Children: []*Node{{
			Name: "document.pdf",
			Path: "root/document.pdf",
			Size: 12,
			Type: "file",
			Ext:  ".pdf",
			MIME: "application/pdf",
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		"const MaterialFileIcons = (() => {",
		"function makeFileIcon(node)",
		`MaterialFileIcons.getIcon(lookupName)`,
		`const REPORT_DATA_PAYLOAD = `,
	} {
		if !strings.Contains(report, needle) {
			t.Fatalf("report missing %q", needle)
		}
	}
}

func TestReportUsesColorfulTreemapFavicon(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `<link rel="icon" type="image/svg+xml" href="` + faviconDataURI() + `">`
	if !strings.Contains(report, want) {
		t.Fatal("report does not contain the embedded webdiskstat favicon")
	}
	for _, color := range []string{"#0ea5e9", "#8b5cf6", "#22c55e", "#f97316", "#ec4899"} {
		if !strings.Contains(faviconSVG, color) {
			t.Fatalf("favicon is missing treemap color %q", color)
		}
	}
	if count := strings.Count(report, `<link rel="icon"`); count != 1 {
		t.Fatalf("report has %d favicon links, want 1", count)
	}
}

func TestMaterialFileIconLookupUsesFilenameBeforeMIME(t *testing.T) {
	report, err := renderReport(&Node{
		Name: "root",
		Path: "root",
		Type: "dir",
		Children: []*Node{{
			Name: "script.py",
			Path: "root/script.py",
			Size: 12,
			Type: "file",
			Ext:  ".py",
			MIME: "text/x-python",
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`const name = String(node.name || "").trim();`,
		`if (name) return name;`,
		`const path = String(node.path || "").trim();`,
		`return parts.length ? parts[parts.length - 1] : path;`,
		`return ext && ext !== "[no extension]" ? ` + "`file${ext.startsWith(\".\") ? ext : \".\" + ext}`" + ` : "file";`,
		`MaterialFileIcons.getIcon(lookupName)`,
	} {
		if !strings.Contains(report, needle) {
			t.Fatalf("report missing filename-based material icon lookup %q", needle)
		}
	}
	if strings.Contains(report, `if (MIME_FAMILY_ICON_FILENAMES.has(family)) return MIME_FAMILY_ICON_FILENAMES.get(family);`) {
		t.Fatal("file icon lookup still maps MIME families before using the filename")
	}
}

func TestReportEmbedsNotoSansFont(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`@font-face{font-family:"Noto Sans";font-style:normal;font-weight:400`,
		`@font-face{font-family:"Noto Sans";font-style:normal;font-weight:700`,
		`data:font/ttf;base64,`,
		`font-family:"Noto Sans",ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif`,
	} {
		if !strings.Contains(report, needle) {
			t.Fatalf("report missing embedded Noto Sans font code %q", needle)
		}
	}
	if strings.Contains(report, "font-family:Inter,") ||
		strings.Contains(report, "font-family:Lato") ||
		strings.Contains(report, `font-family:"Roboto Mono"`) ||
		strings.Contains(report, "font-family:Merriweather") {
		t.Fatal("report still uses an old primary UI font")
	}
}

func TestTreeColumnsCanBeResized(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`const TREE_COLUMN_WIDTH_STORAGE_KEY = "webdiskstat-tree-column-widths";`,
		`columnWidths: {}`,
		`function readStoredTreeColumnWidths()`,
		`function setTreeColumnWidth(column, width)`,
		`function beginTreeColumnResize(event, column, handle)`,
		`function makeTreeColumnResizer(column)`,
		`function makeTreeHeaderCell(column, content)`,
		`return width ? width + "px" : column.grid;`,
		`return makeTreeHeaderCell(column, nameHead);`,
		`state.columnWidths = readStoredTreeColumnWidths();`,
		`.tree-column-resizer`,
		`body.resizing-tree-column`,
	} {
		if !strings.Contains(report, needle) {
			t.Fatalf("report missing tree column resizing code %q", needle)
		}
	}
	if strings.Contains(report, `const template = columns.map(column => column.grid).join(" ");`) {
		t.Fatal("tree column layout still ignores saved column widths")
	}
}

func TestColumnSettingsUsesDistinctColumnIcon(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`icon column-settings-icon`,
		`frame.setAttribute("width", "19");`,
		`"M8.5 4v16"`,
		`"M10.5 14h2.5"`,
		`button.title = "Column settings";`,
	} {
		if !strings.Contains(report, needle) {
			t.Fatalf("report missing column settings icon code %q", needle)
		}
	}
	if strings.Contains(report, `"M4 5h16"`) {
		t.Fatal("report still contains the generic grid icon")
	}
}

func TestThemeSwitcherUsesSingleCelestialControl(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`theme-switch theme-switcher-v2`,
		`class="moon-star"`,
		`.theme-switch.theme-switcher-v2{width:36px;height:36px`,
		`.theme-input:checked+.theme-switcher-v2 .sun-icon`,
		`const nextTheme = normalized === "light" ? "dark" : "light";`,
		`aria-label="Switch to light theme"`,
		`@media(prefers-reduced-motion:reduce)`,
	} {
		if !strings.Contains(report, needle) {
			t.Fatalf("report missing theme switcher code %q", needle)
		}
	}
	if strings.Contains(report, `<span class="theme-knob"></span>`) {
		t.Fatal("report still renders the old sliding theme knob")
	}
}

func TestEnteringDirectoryFocusesFirstBrowserRow(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`function setCurrent(node, updateUrl = true, focusFirstRow = false)`,
		`if (focusFirstRow) focusFirstTreeRow();`,
		`function focusFirstTreeRow()`,
		`const first = currentTreeChildren()[0];`,
		`el.tree.scrollTop = 0;`,
		`row.focus({ preventScroll: true });`,
		`setCurrent(state.selected, true, true);`,
		`if (child.type === "dir") setCurrent(child, true, true);`,
		`if (node.type === "dir") setCurrent(node, true, true);`,
	} {
		if !strings.Contains(report, needle) {
			t.Fatalf("report missing first-row focus behavior %q", needle)
		}
	}
}

func TestReportShowsStagedLoadingProgress(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`class="loading-progress"`,
		`id="loadingProgressFill"`,
		`id="loadingTasks"`,
		`const loadingSteps = [`,
		`{ key: "payload", label: "Decode embedded payload" }`,
		`{ key: "decrypt", label: "Decrypt scan data" }`,
		`{ key: "decompress", label: "Decompress scan data" }`,
		`{ key: "index", label: "Build directory index" }`,
		`{ key: "render", label: "Render interface" }`,
		`function buildLoadingTaskList()`,
		`function setLoadingStep(stepKey, status = "active")`,
		`setLoadingStep("decompress", "active");`,
		`setLoadingStep("index", "done");`,
		`setLoadingStep("render", "done");`,
		`buildLoadingTaskList();`,
	} {
		if !strings.Contains(report, needle) {
			t.Fatalf("report missing loading progress code %q", needle)
		}
	}
	if strings.Contains(report, `id="loadingSubtitle">Preparing disk usage view</div></div></div>`) {
		t.Fatal("report still uses the single-step loading panel without a task list")
	}
}

func TestFocusedBrowserRowUsesSubtleOutline(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	const subtle = `.row:focus-visible{outline:1px solid var(--row-active-line);outline-offset:-1px}`
	if !strings.Contains(report, subtle) {
		t.Fatalf("report missing subtle focused-row style %q", subtle)
	}
	if strings.Contains(report, `.row:focus-visible,.tile:focus-visible,.top-file-row:focus-visible{outline:3px`) {
		t.Fatal("file-browser rows still use the heavy shared focus outline")
	}
}

func TestTreeColumnsHaveSubtleSeparators(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`.tree-header-cell.with-separator{box-shadow:1px 0 0 color-mix(in srgb,var(--line) 68%,transparent)}.row>*:not(:last-child){box-shadow:1px 0 0 color-mix(in srgb,var(--line) 30%,transparent)}`,
		`const columns = visibleTreeColumns();`,
		`column.hasSeparator = index < columns.length - 1;`,
		`if (column.hasSeparator) cell.classList.add("with-separator");`,
	} {
		if !strings.Contains(report, needle) {
			t.Fatalf("report missing subtle tree column separator code %q", needle)
		}
	}
}

func TestTreeTableUsesSmallerFont(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`.tree-header{font-size:10px}`,
		`.row{font-size:12px}`,
		`.row-kind{font-size:9px}`,
		`.row-count,.row-size,.row-modified,.row-pct{font-size:11px}`,
	} {
		if !strings.Contains(report, needle) {
			t.Fatalf("report missing smaller tree table font code %q", needle)
		}
	}
}

func TestTopFilesRenderKeepsNameAndPath(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "main.append(name, path);\n    row.append(makeFileIcon(file), main, size);") {
		t.Fatal("top file rows do not append filename/path before adding the row")
	}
	start := strings.Index(report, "function renderHomePanel()")
	end := strings.Index(report, "function renderDetails()")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("could not locate renderHomePanel in report")
	}
	renderHomePanel := report[start:end]
	if count := strings.Count(renderHomePanel, "main.append(name, path);"); count != 1 {
		t.Fatalf("top file row appends filename/path %d times, want 1", count)
	}
	if strings.Contains(report, "path.textContent = file.path || file.name;\n\n    row.append(makeFileIcon(file), main, size);") {
		t.Fatal("top file row appends an empty main container")
	}
}

func TestReportAddsInlinePathCopyButtons(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`function showCopyPathMessage(button, message = "Path copied")`,
		`function makeCopyPathButton(path, label = "Copy path")`,
		`function renderPathWithCopy(container, path, label = "Copy path")`,
		`showCopyPathMessage(button, "Path copied")`,
		`.copy-path-message`,
		`border:1px solid color-mix(in srgb,var(--accent-2) 42%,transparent)`,
		`background:color-mix(in srgb,var(--accent-2) 14%,transparent)`,
		`.copy-path-message::before{content:"\2713"`,
		`renderPathWithCopy(el.detailPath, node.path || node.name, "Copy detail path");`,
		`path.append(pathText, makeCopyPathButton(filePath, "Copy file path"));`,
		`.inline-copy-path-icon`,
		`html[data-theme="light"] .copy-path-icon,html[data-theme="light"] .inline-copy-path-icon{filter:none}`,
	} {
		if !strings.Contains(report, needle) {
			t.Fatalf("report missing inline path copy code %q", needle)
		}
	}
	if strings.Contains(report, ".copy-toast") || strings.Contains(report, "showCopyToast") {
		t.Fatal("report still uses the bottom-corner copy toast")
	}
}

func TestBreadcrumbsUseShortenedTailForLongPaths(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`const tailCount = 4;`,
		`rootButton.appendChild(makeHomeIcon());`,
		`ellipsis.textContent = ".....";`,
		`if (firstVisibleIndex > 0 && index === 0) return;`,
		`if (index < firstVisibleIndex) return;`,
	} {
		if !strings.Contains(report, needle) {
			t.Fatalf("report missing breadcrumb shortening code %q", needle)
		}
	}
	if strings.Contains(report, `if (el.crumbs.scrollWidth <= el.crumbs.clientWidth) return;

  ellipsis.hidden = false`) {
		t.Fatal("breadcrumbs still wait for overflow before hiding parent directories")
	}
}

func TestBreadcrumbCopyButtonHiddenOnHomePath(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`id="copyPathButton" class="copy-path-btn" type="button" title="Copy current path" aria-label="Copy current path" hidden`,
		`.copy-path-btn[hidden]{display:none}`,
		`const canCopyPath = state.current !== DATA && Boolean(fullPath);`,
		`el.copyPathButton.hidden = !canCopyPath;`,
		`if (state.current === DATA) return;`,
	} {
		if !strings.Contains(report, needle) {
			t.Fatalf("report missing home copy button guard %q", needle)
		}
	}
}

func TestTreemapInnerBordersAreSubtle(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`box-shadow:inset 0 0 0 1px rgba(255,255,255,0.40),inset 0 0 0 999px rgba(255,255,255,0.04)`,
		`.tile-children{position:absolute;overflow:hidden;border-radius:2px}`,
		`.tile.nested{border-width:.5px;border-radius:2px}`,
	} {
		if !strings.Contains(report, needle) {
			t.Fatalf("report missing subtle treemap border style %q", needle)
		}
	}
	if strings.Contains(report, `.tile-children{position:absolute;overflow:hidden;border-radius:5px}`) {
		t.Fatal("treemap child layer still uses 5px radius")
	}
}

func TestTreemapUsesMIMETypeColors(t *testing.T) {
	report, err := renderReport(&Node{
		Name: "root",
		Path: "root",
		Type: "dir",
		Children: []*Node{
			{Name: "photo.jpg", Path: "root/photo.jpg", Size: 80, Type: "file", Ext: ".jpg", MIME: "image/jpeg"},
			{Name: "notes.txt", Path: "root/notes.txt", Size: 20, Type: "file", Ext: ".txt", MIME: "text/plain"},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`const TREEMAP_DIRECTORY_COLORS = [`,
		`"#2563EB", "#4F46E5", "#7C3AED", "#A21CAF"`,
		`"#65A30D", "#16A34A", "#0D9488", "#0891B2"`,
		`const TREEMAP_MIME_COLORS = new Map([`,
		`["image", "#0EA5E9"]`,
		`["video", "#8B5CF6"]`,
		`["audio", "#EC4899"]`,
		`["code", "#14B8A6"]`,
		`["archive", "#F97316"]`,
		`["document", "#3B82F6"]`,
		`["binary", "#64748B"]`,
		`["application/pdf", "pdf"]`,
		`const treemapMimeWeightsCache = new WeakMap();`,
		`function treemapMimeCategoryForFile(node)`,
		`function treemapMimeWeightsForNode(node)`,
		`function dominantTreemapMimeCategory(node)`,
		`function directoryTreemapBranch(node)`,
		`if (node.type === "dir") return directoryTreemapColorFor(node);`,
		`const category = treemapMimeCategoryForFile(node);`,
		`.tile-label{background:linear-gradient(90deg,rgba(2,6,23,.56)`,
	} {
		if !strings.Contains(report, needle) {
			t.Fatalf("report missing MIME treemap color code %q", needle)
		}
	}
	if strings.Contains(report, `if (node.type !== "dir") return colorFor(node);`) {
		t.Fatal("treemap still uses extension color for file tiles before MIME")
	}
}

func TestReportUsesLazyMemoryBudgetedSearch(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		"const SEARCH_INDEX_FILE_LIMIT = 5000000;",
		"const SEARCH_INDEX_CHARACTER_LIMIT = 512000000;",
		"Search disabled for reports over 5,000,000 files",
		"setSearchAvailability(counts.files);",
		"function scheduleSearchIndexBuild()",
		"Preparing memory-efficient search...",
		`if (node.type === "dir") byPath.set(node.path || node.name, node);`,
		"searchIndex.push(node);",
		`REPORT_DATA_PAYLOAD.payload = "";`,
		"materialIconCache.size > 256",
		"let hiddenSize = 0;",
	} {
		if !strings.Contains(report, needle) {
			t.Fatalf("report missing %q", needle)
		}
	}
	for _, obsolete := range []string{
		"const byId = new Map();",
		"const parent = new Map();",
		"const searchCandidateIndex = new Map();",
		"function countReportFiles(root)",
		"function scheduleSearchCandidateIndexBuild()",
		"const hidden = entries.slice(entryLimit);",
	} {
		if strings.Contains(report, obsolete) {
			t.Fatalf("report still contains memory-heavy code %q", obsolete)
		}
	}
	if count := strings.Count(report, "scheduleSearchIndexBuild();"); count != 1 {
		t.Fatalf("search index build is scheduled %d times, want one on-demand call", count)
	}
}

func TestReportUsesLocalAssetsAndAccessibleControls(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`TOP_FILE_INDEX_LIMIT = 50`,
		`function enhanceReportAccessibility()`,
		`element.setAttribute("role", "button");`,
		`el.searchInput.setAttribute("role", "combobox");`,
		`id="reportPasswordDialog"`,
		`function requestReportPassword()`,
		`const password = await requestReportPassword();`,
		`Couldn't copy. Select the path manually.`,
		`background:linear-gradient(135deg,#fbbf24,#f59e0b)`,
	} {
		if !strings.Contains(report, needle) {
			t.Fatalf("report missing %q", needle)
		}
	}
	for _, remoteAsset := range []string{
		"https://cdn-icons-png.flaticon.com",
		"https://img.icons8.com",
	} {
		if strings.Contains(report, remoteAsset) {
			t.Fatalf("report still contains remote asset %q", remoteAsset)
		}
	}
	if strings.Contains(report, "window.prompt") {
		t.Fatal("report still uses the unmasked native password prompt")
	}
}
