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

func TestTreeColumnsHaveSubtleSeparators(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`.tree-header-cell.with-separator,.row>*:not(:last-child){box-shadow:1px 0 0 color-mix(in srgb,var(--line) 30%,transparent)}`,
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
		`box-shadow:inset 0 0 0 1px rgba(255,255,255,0.48),inset 0 0 0 999px rgba(255,255,255,0.08)`,
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
		`const TREEMAP_MIME_COLORS = new Map([`,
		`["image", "hsl(197, 76%, 43%)"]`,
		`["application/pdf", "pdf"]`,
		`const treemapMimeWeightsCache = new WeakMap();`,
		`function treemapMimeCategoryForFile(node)`,
		`function treemapMimeWeightsForNode(node)`,
		`function dominantTreemapMimeCategory(node)`,
		`const category = node.type === "dir" ? dominantTreemapMimeCategory(node) : treemapMimeCategoryForFile(node);`,
		`return node.type === "dir" ? directoryTreemapColorFor(node) : colorFor(node);`,
	} {
		if !strings.Contains(report, needle) {
			t.Fatalf("report missing MIME treemap color code %q", needle)
		}
	}
	if strings.Contains(report, `if (node.type !== "dir") return colorFor(node);`) {
		t.Fatal("treemap still uses extension color for file tiles before MIME")
	}
}

func TestReportDisablesSearchAboveFileLimit(t *testing.T) {
	report, err := renderReport(&Node{Name: "root", Path: "root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		"const SEARCH_FILE_DISABLE_LIMIT = 1000000;",
		"function countReportFiles(root)",
		"Search disabled for reports over 1,000,000 files",
		"if (!searchDisabled) searchIndex.push(entry);",
		"if (!searchDisabled) scheduleSearchCandidateIndexBuild();",
	} {
		if !strings.Contains(report, needle) {
			t.Fatalf("report missing %q", needle)
		}
	}
}
