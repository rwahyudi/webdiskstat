package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

const appTitle = "webdiskstat"

var (
	generatedTitlePattern = regexp.MustCompile(`<title>.*?</title>`)
	generatedTimePattern  = regexp.MustCompile(`<time class="generated" datetime="[^"]*">Generated .*?</time>`)
	reportSizePattern     = regexp.MustCompile(`HTML file: [^<]+`)
	latoFontFacePattern   = regexp.MustCompile(`@font-face\{font-family:Lato;[^}]+\}(@font-face\{font-family:Lato;[^}]+\})?`)
	robotoFontFacePattern = regexp.MustCompile(`@font-face\{font-family:"Roboto Mono";[^}]+\}(@font-face\{font-family:"Roboto Mono";[^}]+\})?`)
	merriFontFacePattern  = regexp.MustCompile(`@font-face\{font-family:Merriweather;[^}]+\}(@font-face\{font-family:Merriweather;[^}]+\})?`)
)

func renderReport(root *Node, password *string) (string, error) {
	payload, err := reportDataPayload(root, password)
	if err != nil {
		return "", err
	}
	now := time.Now().Local()
	generatedISO := now.Format(time.RFC3339)
	generatedDisplay := now.Format("2006-01-02 15:04:05 MST")

	report, err := replacePayload(optimizedReportTemplate(), payload)
	if err != nil {
		return "", err
	}
	report = replaceGeneratedMetadata(report, generatedISO, generatedDisplay)
	report = replaceSecurityStatus(report, password != nil)
	report = fillReportSize(report)
	return report, nil
}

func optimizedReportTemplate() string {
	report := reportTemplate
	report = replaceEmbeddedFonts(report)
	report = replaceReportDataLoader(report)
	report = replaceSearchIndexBuild(report)
	report = replaceFolderIcon(report)
	report = replaceMaterialFileIcons(report)
	report = replaceBreadcrumbControls(report)
	report = replaceTreemapStyles(report)
	report = replaceTreemapMimeColors(report)
	report = replaceTreeColumnResizing(report)
	report = replaceTreeColumnSeparators(report)
	report = replaceTreeTableFontSize(report)
	if !strings.Contains(report, "  topFiles: [],") {
		report = strings.Replace(
			report,
			"  topFilesLimit: 10,\n  treemapTileCap: DEFAULT_TREEMAP_TILE_CAP,",
			"  topFilesLimit: 10,\n  topFiles: [],\n  treemapTileCap: DEFAULT_TREEMAP_TILE_CAP,",
			1,
		)
	}
	if !strings.Contains(report, "function buildTopFilesIndex(root)") {
		report = strings.Replace(
			report,
			`function collectFiles(node, files) {
  if (node.type !== "dir") {
    if (node.size > 0) files.push(node);
    return;
  }
  (node.children || []).forEach(child => collectFiles(child, files));
}
`,
			`function collectFiles(node, files) {
  if (node.type !== "dir") {
    if (node.size > 0) files.push(node);
    return;
  }
  (node.children || []).forEach(child => collectFiles(child, files));
}

function buildTopFilesIndex(root) {
  const files = [];
  collectFiles(root, files);
  files.sort((a, b) => b.size - a.size);
  return files;
}
`,
			1,
		)
	}
	report = strings.Replace(
		report,
		`  const files = [];
  collectFiles(DATA, files);
  files.sort((a, b) => b.size - a.size);
  state.topFilesLimit = normalizeTopFilesLimit(state.topFilesLimit);`,
		`  const files = state.topFiles;
  state.topFilesLimit = normalizeTopFilesLimit(state.topFilesLimit);`,
		1,
	)
	if !strings.Contains(report, "  state.topFiles = buildTopFilesIndex(DATA);") {
		report = strings.Replace(
			report,
			`  DATA = root;
  walk(DATA, null);
  state.current = DATA;`,
			`  DATA = root;
  walk(DATA, null);
  state.topFiles = buildTopFilesIndex(DATA);
  state.current = DATA;`,
			1,
		)
	}
	return report
}

func replaceTreeTableFontSize(report string) string {
	const tableFontCSS = `.tree-header{font-size:10px}.row{font-size:12px}.row-kind{font-size:9px}.row-count,.row-size,.row-modified,.row-pct{font-size:11px}`
	if strings.Contains(report, tableFontCSS) {
		return report
	}
	return strings.Replace(report, `</style>`, tableFontCSS+`</style>`, 1)
}

func replaceTreeColumnSeparators(report string) string {
	const separatorCSS = `.tree-header-cell.with-separator,.row>*:not(:last-child){box-shadow:1px 0 0 color-mix(in srgb,var(--line) 30%,transparent)}`
	if !strings.Contains(report, separatorCSS) {
		report = strings.Replace(report, `.tree-column-resizer{`, separatorCSS+`.tree-column-resizer{`, 1)
	}
	report = strings.Replace(
		report,
		`  cell.dataset.column = column.key;
  cell.append(content, makeTreeColumnResizer(column));`,
		`  cell.dataset.column = column.key;
  if (column.hasSeparator) cell.classList.add("with-separator");
  cell.append(content, makeTreeColumnResizer(column));`,
		1,
	)
	report = strings.Replace(
		report,
		`  const cells = visibleTreeColumns().map(column => {
    if (column.key === "name") return makeTreeHeaderCell(column, nameHead);
    if (column.sortKey) return makeTreeHeaderCell(column, makeHeaderButton(column.label, column.sortKey, column.numeric));
    return makeTreeHeaderCell(column, makeHeaderLabel(column.label, column.numeric));
  });`,
		`  const columns = visibleTreeColumns();
  const cells = columns.map((column, index) => {
    column.hasSeparator = index < columns.length - 1;
    if (column.key === "name") return makeTreeHeaderCell(column, nameHead);
    if (column.sortKey) return makeTreeHeaderCell(column, makeHeaderButton(column.label, column.sortKey, column.numeric));
    return makeTreeHeaderCell(column, makeHeaderLabel(column.label, column.numeric));
  });`,
		1,
	)
	return report
}

func replaceTreeColumnResizing(report string) string {
	if !strings.Contains(report, ".tree-column-resizer") {
		report = strings.Replace(
			report,
			`</style>`,
			`.tree-header-cell{min-width:0;position:relative;display:flex;align-items:center}.tree-header-cell.numeric{justify-content:flex-end}.tree-header-cell>.tree-sort,.tree-header-cell>.tree-label{flex:1 1 auto}.tree-column-resizer{position:absolute;top:0;right:-5px;z-index:8;width:10px;height:100%;border:0;background:transparent;padding:0;cursor:col-resize;touch-action:none}.tree-column-resizer::after{content:"";position:absolute;top:7px;bottom:7px;left:4px;width:2px;border-radius:999px;background:transparent}.tree-column-resizer:hover::after,.tree-column-resizer:focus-visible::after,.tree-column-resizer.dragging::after{background:var(--accent)}body.resizing-tree-column{cursor:col-resize;user-select:none}body.resizing-tree-column *{cursor:col-resize !important}</style>`,
			1,
		)
	}
	if !strings.Contains(report, `const TREE_COLUMN_WIDTH_STORAGE_KEY = "webdiskstat-tree-column-widths";`) {
		report = strings.Replace(
			report,
			`const TREE_COLUMN_STORAGE_KEY = "webdiskstat-tree-columns";`,
			`const TREE_COLUMN_STORAGE_KEY = "webdiskstat-tree-columns";
const TREE_COLUMN_WIDTH_STORAGE_KEY = "webdiskstat-tree-column-widths";`,
			1,
		)
	}
	if !strings.Contains(report, `columnWidths: {},`) {
		report = strings.Replace(
			report,
			`  searchActiveIndex: -1,
  visibleColumns: { ...DEFAULT_TREE_COLUMNS }`,
			`  searchActiveIndex: -1,
  visibleColumns: { ...DEFAULT_TREE_COLUMNS },
  columnWidths: {}`,
			1,
		)
	}
	if !strings.Contains(report, "function readStoredTreeColumnWidths()") {
		report = strings.Replace(
			report,
			`function storeTreeColumns() {
  try {
    localStorage.setItem(TREE_COLUMN_STORAGE_KEY, JSON.stringify(state.visibleColumns));
  } catch (error) {
    // Ignore storage failures in strict file contexts.
  }
}
`,
			`function storeTreeColumns() {
  try {
    localStorage.setItem(TREE_COLUMN_STORAGE_KEY, JSON.stringify(state.visibleColumns));
  } catch (error) {
    // Ignore storage failures in strict file contexts.
  }
}

function readStoredTreeColumnWidths() {
  const widths = {};
  try {
    const stored = JSON.parse(localStorage.getItem(TREE_COLUMN_WIDTH_STORAGE_KEY) || "{}");
    TREE_COLUMNS.forEach(column => {
      const width = Number(stored[column.key]);
      if (Number.isFinite(width) && width >= column.minWidth) widths[column.key] = width;
    });
  } catch (error) {
    // Column widths are optional; the report still works when storage is unavailable.
  }
  return widths;
}

function storeTreeColumnWidths() {
  try {
    localStorage.setItem(TREE_COLUMN_WIDTH_STORAGE_KEY, JSON.stringify(state.columnWidths));
  } catch (error) {
    // Ignore storage failures in strict file contexts.
  }
}

function treeColumnWidth(column) {
  const width = Number(state.columnWidths[column.key]);
  return Number.isFinite(width) ? Math.max(column.minWidth, width) : 0;
}
`,
			1,
		)
	}
	report = strings.Replace(
		report,
		"function applyTreeColumnLayout() {\n"+
			"  const columns = visibleTreeColumns();\n"+
			"  const template = columns.map(column => column.grid).join(\" \");\n"+
			"  const minWidth = columns.reduce((total, column) => total + column.minWidth, 0) +\n"+
			"    Math.max(0, columns.length - 1) * 8 +\n"+
			"    22;\n"+
			"  el.tree.style.setProperty(\"--tree-columns\", template);\n"+
			"  el.tree.style.setProperty(\"--tree-min-width\", `${Math.max(300, minWidth)}px`);\n"+
			"}\n",
		`function applyTreeColumnLayout() {
  const columns = visibleTreeColumns();
  const template = columns.map(column => {
    const width = treeColumnWidth(column);
    return width ? width + "px" : column.grid;
  }).join(" ");
  const minWidth = columns.reduce((total, column) => total + (treeColumnWidth(column) || column.minWidth), 0) +
    Math.max(0, columns.length - 1) * 8 +
    22;
  el.tree.style.setProperty("--tree-columns", template);
  el.tree.style.setProperty("--tree-min-width", Math.max(300, minWidth) + "px");
}
`,
		1,
	)
	if !strings.Contains(report, "function setTreeColumnWidth(column, width)") {
		report = strings.Replace(
			report,
			`function makeTreeColumnsButton(menu) {`,
			`function setTreeColumnWidth(column, width) {
  state.columnWidths[column.key] = Math.max(column.minWidth, Math.round(width));
  applyTreeColumnLayout();
}

function commitTreeColumnWidth(column, width) {
  setTreeColumnWidth(column, width);
  storeTreeColumnWidths();
}

function beginTreeColumnResize(event, column, handle) {
  event.preventDefault();
  event.stopPropagation();
  const startX = event.clientX;
  const startWidth = treeColumnWidth(column) || Math.max(column.minWidth, handle.parentElement.getBoundingClientRect().width);
  handle.classList.add("dragging");
  document.body.classList.add("resizing-tree-column");
  if (handle.setPointerCapture) handle.setPointerCapture(event.pointerId);

  const onMove = moveEvent => {
    const width = startWidth + moveEvent.clientX - startX;
    setTreeColumnWidth(column, width);
  };
  const onEnd = endEvent => {
    window.removeEventListener("pointermove", onMove);
    window.removeEventListener("pointerup", onEnd);
    window.removeEventListener("pointercancel", onEnd);
    handle.classList.remove("dragging");
    document.body.classList.remove("resizing-tree-column");
    if (handle.releasePointerCapture) handle.releasePointerCapture(endEvent.pointerId);
    storeTreeColumnWidths();
  };
  window.addEventListener("pointermove", onMove);
  window.addEventListener("pointerup", onEnd);
  window.addEventListener("pointercancel", onEnd);
}

function handleTreeColumnResizeKey(event, column) {
  let delta = 0;
  if (event.key === "ArrowLeft") delta = -12;
  if (event.key === "ArrowRight") delta = 12;
  if (!delta) return;
  event.preventDefault();
  event.stopPropagation();
  commitTreeColumnWidth(column, (treeColumnWidth(column) || column.minWidth) + delta);
}

function makeTreeColumnResizer(column) {
  const handle = document.createElement("button");
  handle.type = "button";
  handle.className = "tree-column-resizer";
  handle.title = "Resize " + (column.menuLabel || column.label) + " column";
  handle.setAttribute("aria-label", handle.title);
  handle.addEventListener("pointerdown", event => beginTreeColumnResize(event, column, handle));
  handle.addEventListener("keydown", event => handleTreeColumnResizeKey(event, column));
  return handle;
}

function makeTreeHeaderCell(column, content) {
  const cell = document.createElement("div");
  cell.className = column.numeric ? "tree-header-cell numeric" : "tree-header-cell";
  cell.dataset.column = column.key;
  cell.append(content, makeTreeColumnResizer(column));
  return cell;
}

function makeTreeColumnsButton(menu) {`,
			1,
		)
	}
	report = strings.Replace(
		report,
		`  const cells = visibleTreeColumns().map(column => {
    if (column.key === "name") return nameHead;
    if (column.sortKey) return makeHeaderButton(column.label, column.sortKey, column.numeric);
    return makeHeaderLabel(column.label, column.numeric);
  });
  header.append(...cells, columnsMenu);`,
		`  const cells = visibleTreeColumns().map(column => {
    if (column.key === "name") return makeTreeHeaderCell(column, nameHead);
    if (column.sortKey) return makeTreeHeaderCell(column, makeHeaderButton(column.label, column.sortKey, column.numeric));
    return makeTreeHeaderCell(column, makeHeaderLabel(column.label, column.numeric));
  });
  header.append(...cells, columnsMenu);`,
		1,
	)
	report = strings.Replace(
		report,
		`  state.visibleColumns = readStoredTreeColumns();
  applyTreeColumnLayout();`,
		`  state.visibleColumns = readStoredTreeColumns();
  state.columnWidths = readStoredTreeColumnWidths();
  applyTreeColumnLayout();`,
		1,
	)
	return report
}

func replaceEmbeddedFonts(report string) string {
	report = strings.ReplaceAll(
		report,
		`font-family:Inter,ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif`,
		`font-family:"Noto Sans",ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif`,
	)
	report = strings.ReplaceAll(
		report,
		`font-family:Lato,ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif`,
		`font-family:"Noto Sans",ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif`,
	)
	report = strings.ReplaceAll(
		report,
		`font-family:"Roboto Mono",ui-monospace,SFMono-Regular,Menlo,Consolas,"Liberation Mono",monospace`,
		`font-family:"Noto Sans",ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif`,
	)
	report = strings.ReplaceAll(
		report,
		`font-family:Merriweather,Georgia,"Times New Roman",serif`,
		`font-family:"Noto Sans",ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif`,
	)
	report = latoFontFacePattern.ReplaceAllString(report, "")
	report = robotoFontFacePattern.ReplaceAllString(report, "")
	report = merriFontFacePattern.ReplaceAllString(report, "")
	if strings.Contains(report, `@font-face{font-family:"Noto Sans";`) {
		return report
	}
	return strings.Replace(report, "<style>", "<style>"+embeddedFontCSS(), 1)
}

func replaceTreemapMimeColors(report string) string {
	const current = `function treemapColorFor(node) {
  if (node.ext === "[other]") return TREEMAP_SMALLER_ENTRIES_COLOR;
  if (node.type !== "dir") return colorFor(node);
  const hash = hashString(pathForNode(node) || node.name || String(node.id));
  const hue = 176 + (hash % 26);
  const saturation = 52 + (Math.floor(hash / 29) % 18);
  const lightness = 28 + (Math.floor(hash / 521) % 24);
  return ` + "`hsl(${hue}, ${saturation}%, ${lightness}%)`" + `;
}
`
	const replacement = `const TREEMAP_MIME_COLORS = new Map([
  ["image", "hsl(197, 76%, 43%)"],
  ["video", "hsl(270, 58%, 50%)"],
  ["audio", "hsl(316, 62%, 48%)"],
  ["text", "hsl(148, 52%, 38%)"],
  ["code", "hsl(168, 68%, 34%)"],
  ["pdf", "hsl(0, 68%, 50%)"],
  ["archive", "hsl(36, 80%, 46%)"],
  ["document", "hsl(218, 58%, 49%)"],
  ["data", "hsl(44, 78%, 45%)"],
  ["binary", "hsl(220, 11%, 46%)"],
  ["font", "hsl(186, 48%, 39%)"]
]);
const TREEMAP_MIME_EXACT_CATEGORIES = new Map([
  ["application/gzip", "archive"],
  ["application/javascript", "code"],
  ["application/json", "data"],
  ["application/msword", "document"],
  ["application/octet-stream", "binary"],
  ["application/pdf", "pdf"],
  ["application/rtf", "document"],
  ["application/vnd.ms-excel", "document"],
  ["application/vnd.ms-powerpoint", "document"],
  ["application/vnd.openxmlformats-officedocument.presentationml.presentation", "document"],
  ["application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "document"],
  ["application/vnd.openxmlformats-officedocument.wordprocessingml.document", "document"],
  ["application/x-7z-compressed", "archive"],
  ["application/x-bzip2", "archive"],
  ["application/x-rar-compressed", "archive"],
  ["application/x-tar", "archive"],
  ["application/xml", "data"],
  ["application/zip", "archive"],
  ["text/css", "code"],
  ["text/html", "code"],
  ["text/javascript", "code"],
  ["text/markdown", "text"],
  ["text/xml", "data"]
]);
const TREEMAP_EXTENSION_CATEGORIES = new Map([
  [".7z", "archive"], [".bz2", "archive"], [".gz", "archive"], [".rar", "archive"], [".tar", "archive"], [".tgz", "archive"], [".zip", "archive"],
  [".csv", "data"], [".json", "data"], [".xml", "data"], [".yaml", "data"], [".yml", "data"],
  [".css", "code"], [".html", "code"], [".js", "code"], [".jsx", "code"], [".ts", "code"], [".tsx", "code"], [".go", "code"], [".py", "code"], [".php", "code"], [".rb", "code"], [".rs", "code"], [".sh", "code"], [".sql", "code"],
  [".doc", "document"], [".docx", "document"], [".ppt", "document"], [".pptx", "document"], [".rtf", "document"], [".xls", "document"], [".xlsx", "document"],
  [".pdf", "pdf"],
  [".bin", "binary"], [".dll", "binary"], [".exe", "binary"], [".o", "binary"], [".so", "binary"],
  [".md", "text"], [".txt", "text"],
  [".otf", "font"], [".ttf", "font"], [".woff", "font"], [".woff2", "font"]
]);
const treemapMimeWeightsCache = new WeakMap();

function normalizeMime(value) {
  return String(value || "").split(";")[0].trim().toLowerCase();
}

function treemapMimeCategoryForFile(node) {
  const mime = normalizeMime(node && node.mime);
  if (mime) {
    if (TREEMAP_MIME_EXACT_CATEGORIES.has(mime)) return TREEMAP_MIME_EXACT_CATEGORIES.get(mime);
    const family = mime.split("/")[0];
    if (TREEMAP_MIME_COLORS.has(family)) return family;
  }
  const ext = String(node && node.ext || "").toLowerCase();
  if (TREEMAP_EXTENSION_CATEGORIES.has(ext)) return TREEMAP_EXTENSION_CATEGORIES.get(ext);
  return "";
}

function treemapMimeWeightsForNode(node) {
  if (!node) return new Map();
  if (treemapMimeWeightsCache.has(node)) return treemapMimeWeightsCache.get(node);
  const weights = new Map();
  if (node.type !== "dir") {
    const category = treemapMimeCategoryForFile(node);
    if (category) weights.set(category, Math.max(1, Number(node.size) || 0));
  } else {
    (node.children || []).forEach(child => {
      treemapMimeWeightsForNode(child).forEach((size, category) => {
        weights.set(category, (weights.get(category) || 0) + size);
      });
    });
  }
  treemapMimeWeightsCache.set(node, weights);
  return weights;
}

function dominantTreemapMimeCategory(node) {
  let bestCategory = "";
  let bestSize = -1;
  treemapMimeWeightsForNode(node).forEach((size, category) => {
    if (size > bestSize || (size === bestSize && category < bestCategory)) {
      bestCategory = category;
      bestSize = size;
    }
  });
  return bestCategory;
}

function directoryTreemapColorFor(node) {
  const hash = hashString(pathForNode(node) || node.name || String(node.id));
  const hue = 176 + (hash % 26);
  const saturation = 52 + (Math.floor(hash / 29) % 18);
  const lightness = 28 + (Math.floor(hash / 521) % 24);
  return ` + "`hsl(${hue}, ${saturation}%, ${lightness}%)`" + `;
}

function treemapColorFor(node) {
  if (node.ext === "[other]") return TREEMAP_SMALLER_ENTRIES_COLOR;
  const category = node.type === "dir" ? dominantTreemapMimeCategory(node) : treemapMimeCategoryForFile(node);
  if (category && TREEMAP_MIME_COLORS.has(category)) return TREEMAP_MIME_COLORS.get(category);
  return node.type === "dir" ? directoryTreemapColorFor(node) : colorFor(node);
}
`
	if strings.Contains(report, current) {
		return strings.Replace(report, current, replacement, 1)
	}
	if strings.Contains(report, "function treemapMimeCategoryForFile(node)") {
		return report
	}
	return strings.Replace(
		report,
		`function treemapColorFor(node) {`,
		replacement+`
function treemapColorForLegacy(node) {`,
		1,
	)
}

func replaceTreemapStyles(report string) string {
	report = strings.ReplaceAll(
		report,
		`box-shadow:inset 0 0 0 2px rgba(255,255,255,0.58),inset 0 0 0 999px rgba(255,255,255,0.10)`,
		`box-shadow:inset 0 0 0 1px rgba(255,255,255,0.48),inset 0 0 0 999px rgba(255,255,255,0.08)`,
	)
	report = strings.ReplaceAll(
		report,
		`.tile-children{position:absolute;overflow:hidden;border-radius:5px}`,
		`.tile-children{position:absolute;overflow:hidden;border-radius:2px}`,
	)
	report = strings.ReplaceAll(
		report,
		`.tile.nested{border-width:1px}`,
		`.tile.nested{border-width:.5px;border-radius:2px}`,
	)
	return report
}

func replaceMaterialFileIcons(report string) string {
	const materialIconLookupFunction = `function materialIconLookupName(node) {
  if (!node || node.type === "dir") return "";
  const name = String(node.name || "").trim();
  if (name) return name;
  const path = String(node.path || "").trim();
  if (path) {
    const parts = path.split("/").filter(Boolean);
    return parts.length ? parts[parts.length - 1] : path;
  }
  const ext = String(node.ext || "").trim();
  return ext && ext !== "[no extension]" ? ` + "`file${ext.startsWith(\".\") ? ext : \".\" + ext}`" + ` : "file";
}
`
	const oldMaterialIconLookupFunction = `function materialIconLookupName(node) {
  if (!node || node.type === "dir") return "";
  const mime = String(node.mime || "").split(";")[0].trim().toLowerCase();
  if (MIME_ICON_FILENAMES.has(mime)) return MIME_ICON_FILENAMES.get(mime);
  const family = mime.split("/")[0];
  if (MIME_FAMILY_ICON_FILENAMES.has(family)) return MIME_FAMILY_ICON_FILENAMES.get(family);
  return node.name || node.ext || "file";
}
`
	if !strings.Contains(report, "const MaterialFileIcons = (() => {") {
		wrapper := "<script>\nconst MaterialFileIcons = (() => {\nconst exports = {};\nconst module = { exports };\n" +
			materialFileIconsBundle +
			"\nreturn module.exports && Object.keys(module.exports).length ? module.exports : exports;\n})();\n</script>\n"
		report = strings.Replace(report, "<script>\nconst REPORT_DATA_PAYLOAD = ", wrapper+"<script>\nconst REPORT_DATA_PAYLOAD = ", 1)
	}
	report = strings.Replace(
		report,
		`.swatch{width:10px;height:10px;border-radius:2px;flex:0 0 auto;background:var(--row-color,#2563eb);box-shadow:inset 0 0 0 1px rgba(0,0,0,0.16)}`,
		`.swatch{width:18px;height:18px;flex:0 0 auto;display:inline-grid;place-items:center;overflow:hidden}.material-file-icon svg{width:100%;height:100%;display:block}`,
		1,
	)
	report = strings.Replace(
		report,
		`.row.file .swatch{border-radius:50%}`,
		`.row.file .swatch{border-radius:0;background:transparent;box-shadow:none}`,
		1,
	)
	report = strings.Replace(
		report,
		`.top-file-row{display:grid;grid-template-columns:minmax(0,1fr) 120px;align-items:center;gap:12px;padding:10px 14px}`,
		`.top-file-row{display:grid;grid-template-columns:20px minmax(0,1fr) 120px;align-items:center;gap:10px;padding:10px 14px}`,
		1,
	)
	if !strings.Contains(report, "const MIME_ICON_FILENAMES = new Map(") {
		report = strings.Replace(
			report,
			`function normalizeSearchText(value) {
  return String(value || "").trim().toLowerCase();
}
`,
			`const MIME_ICON_FILENAMES = new Map([
  ["application/gzip", "archive.gz"],
  ["application/javascript", "file.js"],
  ["application/json", "file.json"],
  ["application/msword", "file.doc"],
  ["application/octet-stream", "file.bin"],
  ["application/pdf", "file.pdf"],
  ["application/rtf", "file.rtf"],
  ["application/vnd.ms-excel", "file.xls"],
  ["application/vnd.ms-powerpoint", "file.ppt"],
  ["application/vnd.openxmlformats-officedocument.presentationml.presentation", "file.pptx"],
  ["application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "file.xlsx"],
  ["application/vnd.openxmlformats-officedocument.wordprocessingml.document", "file.docx"],
  ["application/x-7z-compressed", "archive.7z"],
  ["application/x-bzip2", "archive.bz2"],
  ["application/x-rar-compressed", "archive.rar"],
  ["application/x-tar", "archive.tar"],
  ["application/xml", "file.xml"],
  ["application/zip", "archive.zip"],
  ["text/css", "file.css"],
  ["text/csv", "file.csv"],
  ["text/html", "file.html"],
  ["text/javascript", "file.js"],
  ["text/markdown", "file.md"],
  ["text/plain", "file.txt"],
  ["text/xml", "file.xml"]
]);
const MIME_FAMILY_ICON_FILENAMES = new Map([
  ["audio", "file.mp3"],
  ["image", "file.png"],
  ["text", "file.txt"],
  ["video", "file.mp4"]
]);
const materialIconCache = new Map();

function normalizeSearchText(value) {
  return String(value || "").trim().toLowerCase();
}
`,
			1,
		)
	}
	if !strings.Contains(report, "function makeFileIcon(node)") {
		report = strings.Replace(
			report,
			`function makeParentHeaderButton() {
  const button = document.createElement("button");`,
			materialIconLookupFunction+`
function materialIconSVG(node) {
  const lookupName = materialIconLookupName(node);
  if (!lookupName) return "";
  if (materialIconCache.has(lookupName)) return materialIconCache.get(lookupName);
  const icon = MaterialFileIcons && typeof MaterialFileIcons.getIcon === "function"
    ? MaterialFileIcons.getIcon(lookupName)
    : null;
  const svg = icon && icon.svg ? icon.svg : "";
  materialIconCache.set(lookupName, svg);
  return svg;
}

function makeFileIcon(node) {
  const icon = document.createElement("span");
  icon.className = "swatch";
  if (!node || node.type === "dir") return icon;
  icon.classList.add("material-file-icon");
  icon.innerHTML = materialIconSVG(node);
  return icon;
}

function makeParentHeaderButton() {
  const button = document.createElement("button");`,
			1,
		)
	}
	report = strings.Replace(report, oldMaterialIconLookupFunction, materialIconLookupFunction, 1)
	report = strings.Replace(
		report,
		`  const swatch = document.createElement("span");
  swatch.className = "swatch";`,
		`  const swatch = makeFileIcon(child);`,
		1,
	)
	report = strings.Replace(
		report,
		`    main.append(name, path);
    row.append(main, size);`,
		`    main.append(name, path);
    row.append(makeFileIcon(file), main, size);`,
		1,
	)
	for strings.Contains(report, `    main.append(name, path);
    main.append(name, path);`,
	) {
		report = strings.ReplaceAll(
			report,
			`    main.append(name, path);
    main.append(name, path);`,
			`    main.append(name, path);`,
		)
	}
	if !strings.Contains(report, "main.append(name, path);\n    row.append(makeFileIcon(file), main, size);") {
		report = strings.Replace(
			report,
			`    row.append(makeFileIcon(file), main, size);`,
			`    main.append(name, path);
    row.append(makeFileIcon(file), main, size);`,
			1,
		)
	}
	return report
}

func replaceBreadcrumbControls(report string) string {
	const copyIconURL = "https://cdn-icons-png.flaticon.com/512/1250/1250607.png"
	if !strings.Contains(report, `class="pathbar"`) {
		report = strings.Replace(
			report,
			`<nav id="crumbs" class="crumbs" aria-label="Path"></nav><div class="search"`,
			`<div class="pathbar"><nav id="crumbs" class="crumbs" aria-label="Path"></nav><button id="copyPathButton" class="copy-path-btn" type="button" title="Copy current path" aria-label="Copy current path" hidden><img class="copy-path-icon" src="`+copyIconURL+`" alt="" aria-hidden="true"></button></div><div class="search"`,
			1,
		)
	}
	report = strings.ReplaceAll(
		report,
		`<button id="copyPathButton" class="copy-path-btn" type="button" title="Copy current path" aria-label="Copy current path"><svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg></button>`,
		`<button id="copyPathButton" class="copy-path-btn" type="button" title="Copy current path" aria-label="Copy current path" hidden><img class="copy-path-icon" src="`+copyIconURL+`" alt="" aria-hidden="true"></button>`,
	)
	report = strings.ReplaceAll(
		report,
		`<button id="copyPathButton" class="copy-path-btn" type="button" title="Copy current path" aria-label="Copy current path"><img class="copy-path-icon" src="`+copyIconURL+`" alt="" aria-hidden="true"></button>`,
		`<button id="copyPathButton" class="copy-path-btn" type="button" title="Copy current path" aria-label="Copy current path" hidden><img class="copy-path-icon" src="`+copyIconURL+`" alt="" aria-hidden="true"></button>`,
	)
	if !strings.Contains(report, ".pathbar{") {
		report = strings.Replace(
			report,
			`.crumbs{min-width:0;display:flex;align-items:center;gap:6px;overflow:hidden;white-space:nowrap}.crumb{border:0;background:transparent;color:var(--accent);border-radius:5px;padding:5px 6px;cursor:pointer;overflow:hidden;text-overflow:ellipsis;max-width:260px}`,
			`.pathbar{min-width:0;display:flex;align-items:center;gap:4px;overflow:hidden}.crumbs{min-width:0;max-width:calc(100% - 26px);flex:0 1 auto;display:flex;align-items:center;gap:4px;overflow:hidden;white-space:nowrap}.crumb{border:0;background:transparent;color:var(--accent);border-radius:5px;padding:5px 6px;cursor:pointer;overflow:hidden;text-overflow:ellipsis;max-width:260px}.crumb.ellipsis{flex:0 0 auto;color:var(--muted);max-width:none}.copy-path-btn{width:22px;height:22px;flex:0 0 auto;border:0;border-radius:5px;background:transparent;display:inline-grid;place-items:center;padding:2px;cursor:pointer}.copy-path-btn[hidden]{display:none}.copy-path-icon{width:14px;height:14px;display:block;opacity:.82;filter:brightness(0) invert(1)}.copy-path-btn:hover .copy-path-icon,.copy-path-btn.copied .copy-path-icon{opacity:1}`,
			1,
		)
	}
	report = strings.ReplaceAll(report, `.pathbar{min-width:0;display:flex;align-items:center;gap:6px}`, `.pathbar{min-width:0;display:flex;align-items:center;gap:4px;overflow:hidden}`)
	report = strings.ReplaceAll(report, `.crumbs{min-width:0;flex:1 1 auto;display:flex;align-items:center;gap:6px;overflow:hidden;white-space:nowrap}`, `.crumbs{min-width:0;max-width:calc(100% - 26px);flex:0 1 auto;display:flex;align-items:center;gap:4px;overflow:hidden;white-space:nowrap}`)
	report = strings.ReplaceAll(report, `.copy-path-btn{width:32px;height:32px;flex:0 0 auto;border:1px solid color-mix(in srgb,var(--line) 78%,transparent);border-radius:7px;background:color-mix(in srgb,var(--control) 78%,transparent);color:var(--muted);display:inline-grid;place-items:center;padding:0;cursor:pointer;box-shadow:var(--shadow-soft)}.copy-path-btn:hover,.copy-path-btn.copied{border-color:color-mix(in srgb,var(--accent) 50%,var(--line));color:var(--ink);background:color-mix(in srgb,var(--accent) 12%,var(--control))}`, `.copy-path-btn{width:22px;height:22px;flex:0 0 auto;border:0;border-radius:5px;background:transparent;display:inline-grid;place-items:center;padding:2px;cursor:pointer}.copy-path-btn[hidden]{display:none}.copy-path-icon{width:14px;height:14px;display:block;opacity:.82;filter:brightness(0) invert(1)}.copy-path-btn:hover .copy-path-icon,.copy-path-btn.copied .copy-path-icon{opacity:1}`)
	if !strings.Contains(report, `.copy-path-btn[hidden]{display:none}`) {
		report = strings.Replace(
			report,
			`.copy-path-icon{width:14px;height:14px;display:block;opacity:.82;filter:brightness(0) invert(1)}`,
			`.copy-path-btn[hidden]{display:none}.copy-path-icon{width:14px;height:14px;display:block;opacity:.82;filter:brightness(0) invert(1)}`,
			1,
		)
	}
	report = strings.ReplaceAll(report, `.copy-path-icon{width:14px;height:14px;display:block;opacity:.76}`, `.copy-path-icon{width:14px;height:14px;display:block;opacity:.82;filter:brightness(0) invert(1)}`)
	if !strings.Contains(report, ".inline-copy-path-icon") {
		report = strings.Replace(
			report,
			`.copy-path-btn:hover .copy-path-icon,.copy-path-btn.copied .copy-path-icon{opacity:1}`,
			`.copy-path-btn:hover .copy-path-icon,.copy-path-btn.copied .copy-path-icon{opacity:1}.path-copy-line{min-width:0;display:inline-flex;align-items:center;gap:4px;max-width:100%;vertical-align:middle}.path-copy-text{min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.inline-copy-path-btn{width:18px;height:18px;flex:0 0 auto;border:0;border-radius:4px;background:transparent;display:inline-grid;place-items:center;padding:2px;cursor:pointer}.inline-copy-path-icon{width:12px;height:12px;display:block;opacity:.82;filter:brightness(0) invert(1)}.inline-copy-path-btn:hover .inline-copy-path-icon,.inline-copy-path-btn.copied .inline-copy-path-icon{opacity:1}.copy-path-message{flex:0 0 auto;display:inline-flex;align-items:center;gap:4px;padding:3px 6px;border:1px solid color-mix(in srgb,var(--accent-2) 42%,transparent);border-radius:999px;background:color-mix(in srgb,var(--accent-2) 14%,transparent);color:var(--accent-2);font-size:11px;font-weight:650;line-height:1;white-space:nowrap;opacity:0;transform:translateX(-2px);transition:opacity 140ms ease,transform 140ms ease;pointer-events:none}.copy-path-message::before{content:"\2713";font-weight:800;line-height:1}.copy-path-message.show{opacity:1;transform:translateX(0)}`,
			1,
		)
	}
	report = strings.ReplaceAll(
		report,
		`.copy-toast{position:fixed;right:18px;bottom:42px;z-index:10001;padding:8px 11px;border:1px solid var(--line);border-radius:7px;background:color-mix(in srgb,var(--panel) 94%,black 6%);color:var(--ink);font-size:12px;font-weight:650;box-shadow:var(--shadow),var(--shadow-soft);opacity:0;transform:translateY(6px);transition:opacity 140ms ease,transform 140ms ease;pointer-events:none}.copy-toast.show{opacity:1;transform:translateY(0)}`,
		`.copy-path-message{flex:0 0 auto;display:inline-flex;align-items:center;gap:4px;padding:3px 6px;border:1px solid color-mix(in srgb,var(--accent-2) 42%,transparent);border-radius:999px;background:color-mix(in srgb,var(--accent-2) 14%,transparent);color:var(--accent-2);font-size:11px;font-weight:650;line-height:1;white-space:nowrap;opacity:0;transform:translateX(-2px);transition:opacity 140ms ease,transform 140ms ease;pointer-events:none}.copy-path-message::before{content:"\2713";font-weight:800;line-height:1}.copy-path-message.show{opacity:1;transform:translateX(0)}`,
	)
	report = strings.ReplaceAll(
		report,
		`.copy-path-message{flex:0 0 auto;color:var(--accent-2);font-size:11px;font-weight:650;line-height:1;white-space:nowrap;opacity:0;transform:translateX(-2px);transition:opacity 140ms ease,transform 140ms ease;pointer-events:none}.copy-path-message.show{opacity:1;transform:translateX(0)}`,
		`.copy-path-message{flex:0 0 auto;display:inline-flex;align-items:center;gap:4px;padding:3px 6px;border:1px solid color-mix(in srgb,var(--accent-2) 42%,transparent);border-radius:999px;background:color-mix(in srgb,var(--accent-2) 14%,transparent);color:var(--accent-2);font-size:11px;font-weight:650;line-height:1;white-space:nowrap;opacity:0;transform:translateX(-2px);transition:opacity 140ms ease,transform 140ms ease;pointer-events:none}.copy-path-message::before{content:"\2713";font-weight:800;line-height:1}.copy-path-message.show{opacity:1;transform:translateX(0)}`,
	)
	report = strings.ReplaceAll(report, `.top-file-name,.top-file-path{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}`, `.top-file-name{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.top-file-path{min-width:0;overflow:hidden}`)
	report = strings.ReplaceAll(report, `.detail-path{margin-top:4px;color:var(--muted);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}`, `.detail-path{margin-top:4px;color:var(--muted);min-width:0;overflow:hidden}`)
	if !strings.Contains(report, "copyPathButton: document.getElementById") {
		report = strings.Replace(
			report,
			`  crumbs: document.getElementById("crumbs"),
  themeToggle: document.getElementById("themeToggle"),`,
			`  crumbs: document.getElementById("crumbs"),
  copyPathButton: document.getElementById("copyPathButton"),
  themeToggle: document.getElementById("themeToggle"),`,
			1,
		)
	}
	if !strings.Contains(report, "function compactCrumbs()") {
		report = strings.Replace(
			report,
			`function renderCrumbs() {
  el.crumbs.textContent = "";
  pathToRoot(state.current).forEach((node, index, nodes) => {
    const button = document.createElement("button");
    button.className = index === 0 ? "crumb root" : "crumb";
    if (index === 0) {
      button.appendChild(makeHomeIcon());
    } else {
      button.textContent = node.name;
    }
    button.title = node.path || node.name;
    button.setAttribute("aria-label", index === 0 ? "Root" : node.name);
    button.addEventListener("click", () => setCurrent(node));
    el.crumbs.appendChild(button);
    if (index < nodes.length - 1) {
      const sep = document.createElement("span");
      sep.className = "sep";
      sep.textContent = "/";
      el.crumbs.appendChild(sep);
    }
  });
}
`,
			`function currentPathLabel() {
  return state.current ? state.current.path || state.current.name || "" : "";
}

function makeCrumbSeparator(afterIndex) {
  const sep = document.createElement("span");
  sep.className = "sep";
  sep.dataset.sepAfter = String(afterIndex);
  sep.textContent = "/";
  return sep;
}

function hideCrumbAt(index) {
  const crumb = el.crumbs.querySelector(`+"`"+`.crumb[data-crumb-index="${index}"]`+"`"+`);
  const sep = el.crumbs.querySelector(`+"`"+`.sep[data-sep-after="${index}"]`+"`"+`);
  if (crumb) crumb.hidden = true;
  if (sep) sep.hidden = true;
}

function compactCrumbs() {
  const crumbs = Array.from(el.crumbs.querySelectorAll(".crumb[data-crumb-index]"));
  crumbs.forEach(crumb => {
    crumb.hidden = false;
    crumb.style.maxWidth = "";
  });
  el.crumbs.querySelectorAll(".sep").forEach(sep => { sep.hidden = false; });
  if (el.crumbs.scrollWidth <= el.crumbs.clientWidth) return;
  const current = crumbs[crumbs.length - 1];
  if (current && el.crumbs.scrollWidth > el.crumbs.clientWidth) {
    current.style.maxWidth = Math.max(96, Math.floor(el.crumbs.clientWidth * 0.45)) + "px";
  }
}

function renderCrumbs() {
  el.crumbs.textContent = "";
  const nodes = pathToRoot(state.current);
  const fullPath = currentPathLabel();
  const tailCount = 4;
  const firstVisibleIndex = nodes.length > tailCount + 1 ? nodes.length - tailCount : 0;
  el.crumbs.title = fullPath;
  if (firstVisibleIndex > 0) {
    const root = nodes[0];
    const rootButton = document.createElement("button");
    rootButton.className = "crumb root";
    rootButton.dataset.crumbIndex = "0";
    rootButton.appendChild(makeHomeIcon());
    rootButton.title = fullPath;
    rootButton.setAttribute("aria-label", "Root");
    rootButton.addEventListener("click", () => setCurrent(root));
    el.crumbs.appendChild(rootButton);
    el.crumbs.appendChild(makeCrumbSeparator(0));
    const ellipsis = document.createElement("span");
    ellipsis.className = "crumb ellipsis";
    ellipsis.textContent = ".....";
    ellipsis.title = fullPath;
    ellipsis.setAttribute("aria-label", "Hidden parent directories. Full path: " + fullPath);
    el.crumbs.appendChild(ellipsis);
    el.crumbs.appendChild(makeCrumbSeparator("ellipsis"));
  }
  nodes.forEach((node, index) => {
    if (firstVisibleIndex > 0 && index === 0) return;
    if (index < firstVisibleIndex) return;
    const button = document.createElement("button");
    button.className = index === 0 ? "crumb root" : "crumb";
    button.dataset.crumbIndex = String(index);
    if (index === 0) {
      button.appendChild(makeHomeIcon());
    } else {
      button.textContent = node.name;
    }
    button.title = fullPath;
    button.setAttribute("aria-label", index === 0 ? "Root" : node.name);
    button.addEventListener("click", () => setCurrent(node));
    el.crumbs.appendChild(button);
    if (index < nodes.length - 1) {
      el.crumbs.appendChild(makeCrumbSeparator(index));
    }
  });
  if (el.copyPathButton) {
    el.copyPathButton.title = "Copy " + fullPath;
    el.copyPathButton.setAttribute("aria-label", "Copy current path: " + fullPath);
  }
  requestAnimationFrame(compactCrumbs);
}
`,
			1,
		)
	}
	if !strings.Contains(report, `ellipsis.textContent = ".....";`) {
		report = strings.Replace(
			report,
			`function compactCrumbs() {
  const crumbs = Array.from(el.crumbs.querySelectorAll(".crumb[data-crumb-index]"));
  const ellipsis = el.crumbs.querySelector(".crumb.ellipsis");
  if (!ellipsis) return;
  crumbs.forEach(crumb => {
    crumb.hidden = false;
    crumb.style.maxWidth = "";
  });
  el.crumbs.querySelectorAll(".sep").forEach(sep => { sep.hidden = false; });
  ellipsis.hidden = true;
  const ellipsisSep = el.crumbs.querySelector(".sep.ellipsis-sep");
  if (ellipsisSep) ellipsisSep.hidden = true;
  if (el.crumbs.scrollWidth <= el.crumbs.clientWidth) return;

  ellipsis.hidden = false;
  if (ellipsisSep) ellipsisSep.hidden = false;
  const preferredTailCount = 4;
  const minimumTailCount = 3;
  let hideBefore = Math.max(1, crumbs.length - preferredTailCount);
  for (let index = 1; index < hideBefore; index++) {
    hideCrumbAt(index);
  }
  for (
    let index = hideBefore;
    index < crumbs.length - minimumTailCount && el.crumbs.scrollWidth > el.crumbs.clientWidth;
    index++
  ) {
    hideCrumbAt(index);
  }
  const current = crumbs[crumbs.length - 1];
  if (current && el.crumbs.scrollWidth > el.crumbs.clientWidth) {
    current.style.maxWidth = Math.max(96, Math.floor(el.crumbs.clientWidth * 0.45)) + "px";
  }
}

function renderCrumbs() {
  el.crumbs.textContent = "";
  const nodes = pathToRoot(state.current);
  const fullPath = currentPathLabel();
  el.crumbs.title = fullPath;
  nodes.forEach((node, index) => {
    const button = document.createElement("button");
    button.className = index === 0 ? "crumb root" : "crumb";
    button.dataset.crumbIndex = String(index);
    if (index === 0) {
      button.appendChild(makeHomeIcon());
    } else {
      button.textContent = node.name;
    }
    button.title = fullPath;
    button.setAttribute("aria-label", index === 0 ? "Root" : node.name);
    button.addEventListener("click", () => setCurrent(node));
    el.crumbs.appendChild(button);
    if (index === 0 && nodes.length > 2) {
      el.crumbs.appendChild(makeCrumbSeparator(index));
      const ellipsis = document.createElement("button");
      ellipsis.className = "crumb ellipsis";
      ellipsis.type = "button";
      ellipsis.textContent = "....";
      ellipsis.title = fullPath;
      ellipsis.setAttribute("aria-label", "Hidden parent directories. Full path: " + fullPath);
      ellipsis.hidden = true;
      el.crumbs.appendChild(ellipsis);
      const ellipsisSep = makeCrumbSeparator("ellipsis");
      ellipsisSep.classList.add("ellipsis-sep");
      ellipsisSep.hidden = true;
      el.crumbs.appendChild(ellipsisSep);
    }
    if (index < nodes.length - 1 && !(index === 0 && nodes.length > 2)) {
      el.crumbs.appendChild(makeCrumbSeparator(index));
    }
  });
  if (el.copyPathButton) {
    el.copyPathButton.title = "Copy " + fullPath;
    el.copyPathButton.setAttribute("aria-label", "Copy current path: " + fullPath);
  }
  requestAnimationFrame(compactCrumbs);
}
`,
			`function compactCrumbs() {
  const crumbs = Array.from(el.crumbs.querySelectorAll(".crumb[data-crumb-index]"));
  crumbs.forEach(crumb => {
    crumb.hidden = false;
    crumb.style.maxWidth = "";
  });
  el.crumbs.querySelectorAll(".sep").forEach(sep => { sep.hidden = false; });
  if (el.crumbs.scrollWidth <= el.crumbs.clientWidth) return;
  const current = crumbs[crumbs.length - 1];
  if (current && el.crumbs.scrollWidth > el.crumbs.clientWidth) {
    current.style.maxWidth = Math.max(96, Math.floor(el.crumbs.clientWidth * 0.45)) + "px";
  }
}

function renderCrumbs() {
  el.crumbs.textContent = "";
  const nodes = pathToRoot(state.current);
  const fullPath = currentPathLabel();
  const tailCount = 4;
  const firstVisibleIndex = nodes.length > tailCount + 1 ? nodes.length - tailCount : 0;
  el.crumbs.title = fullPath;
  if (firstVisibleIndex > 0) {
    const root = nodes[0];
    const rootButton = document.createElement("button");
    rootButton.className = "crumb root";
    rootButton.dataset.crumbIndex = "0";
    rootButton.appendChild(makeHomeIcon());
    rootButton.title = fullPath;
    rootButton.setAttribute("aria-label", "Root");
    rootButton.addEventListener("click", () => setCurrent(root));
    el.crumbs.appendChild(rootButton);
    el.crumbs.appendChild(makeCrumbSeparator(0));
    const ellipsis = document.createElement("span");
    ellipsis.className = "crumb ellipsis";
    ellipsis.textContent = ".....";
    ellipsis.title = fullPath;
    ellipsis.setAttribute("aria-label", "Hidden parent directories. Full path: " + fullPath);
    el.crumbs.appendChild(ellipsis);
    el.crumbs.appendChild(makeCrumbSeparator("ellipsis"));
  }
  nodes.forEach((node, index) => {
    if (firstVisibleIndex > 0 && index === 0) return;
    if (index < firstVisibleIndex) return;
    const button = document.createElement("button");
    button.className = index === 0 ? "crumb root" : "crumb";
    button.dataset.crumbIndex = String(index);
    if (index === 0) {
      button.appendChild(makeHomeIcon());
    } else {
      button.textContent = node.name;
    }
    button.title = fullPath;
    button.setAttribute("aria-label", index === 0 ? "Root" : node.name);
    button.addEventListener("click", () => setCurrent(node));
    el.crumbs.appendChild(button);
    if (index < nodes.length - 1) {
      el.crumbs.appendChild(makeCrumbSeparator(index));
    }
  });
  if (el.copyPathButton) {
    el.copyPathButton.title = "Copy " + fullPath;
    el.copyPathButton.setAttribute("aria-label", "Copy current path: " + fullPath);
  }
  requestAnimationFrame(compactCrumbs);
}
`,
			1,
		)
	}
	if !strings.Contains(report, "rootButton.appendChild(makeHomeIcon());") {
		report = strings.Replace(
			report,
			`  if (firstVisibleIndex > 0) {
    const ellipsis = document.createElement("span");
    ellipsis.className = "crumb ellipsis";`,
			`  if (firstVisibleIndex > 0) {
    const root = nodes[0];
    const rootButton = document.createElement("button");
    rootButton.className = "crumb root";
    rootButton.dataset.crumbIndex = "0";
    rootButton.appendChild(makeHomeIcon());
    rootButton.title = fullPath;
    rootButton.setAttribute("aria-label", "Root");
    rootButton.addEventListener("click", () => setCurrent(root));
    el.crumbs.appendChild(rootButton);
    el.crumbs.appendChild(makeCrumbSeparator(0));
    const ellipsis = document.createElement("span");
    ellipsis.className = "crumb ellipsis";`,
			1,
		)
		report = strings.Replace(
			report,
			`  nodes.forEach((node, index) => {
    if (index < firstVisibleIndex) return;`,
			`  nodes.forEach((node, index) => {
    if (firstVisibleIndex > 0 && index === 0) return;
    if (index < firstVisibleIndex) return;`,
			1,
		)
	}
	report = strings.ReplaceAll(
		report,
		`  ellipsis.hidden = false;
  if (ellipsisSep) ellipsisSep.hidden = false;
  for (let index = 1; index < crumbs.length - 1 && el.crumbs.scrollWidth > el.crumbs.clientWidth; index++) {
    hideCrumbAt(index);
  }
  const current = crumbs[crumbs.length - 1];`,
		`  ellipsis.hidden = false;
  if (ellipsisSep) ellipsisSep.hidden = false;
  const preferredTailCount = 4;
  const minimumTailCount = 3;
  let hideBefore = Math.max(1, crumbs.length - preferredTailCount);
  for (let index = 1; index < hideBefore; index++) {
    hideCrumbAt(index);
  }
  for (
    let index = hideBefore;
    index < crumbs.length - minimumTailCount && el.crumbs.scrollWidth > el.crumbs.clientWidth;
    index++
  ) {
    hideCrumbAt(index);
  }
  const current = crumbs[crumbs.length - 1];`,
	)
	report = strings.ReplaceAll(
		report,
		`  if (el.copyPathButton) {
    el.copyPathButton.title = "Copy " + fullPath;
    el.copyPathButton.setAttribute("aria-label", "Copy current path: " + fullPath);
  }`,
		`  if (el.copyPathButton) {
    const canCopyPath = state.current !== DATA && Boolean(fullPath);
    el.copyPathButton.hidden = !canCopyPath;
    if (canCopyPath) {
      el.copyPathButton.title = "Copy " + fullPath;
      el.copyPathButton.setAttribute("aria-label", "Copy current path: " + fullPath);
    }
  }`,
	)
	if !strings.Contains(report, "function copyCurrentPath()") {
		report = strings.Replace(
			report,
			`function hideTooltip() {
  el.tooltip.style.display = "none";
}
`,
			`function hideTooltip() {
  el.tooltip.style.display = "none";
}

function setCopyPathCopiedState() {
  if (!el.copyPathButton) return;
  el.copyPathButton.classList.add("copied");
  const originalTitle = el.copyPathButton.title;
  el.copyPathButton.title = "Copied";
  window.setTimeout(() => {
    el.copyPathButton.classList.remove("copied");
    el.copyPathButton.title = originalTitle;
  }, 1200);
}

async function copyCurrentPath() {
  const path = currentPathLabel();
  if (!path) return;
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(path);
    } else {
      const input = document.createElement("textarea");
      input.value = path;
      input.setAttribute("readonly", "");
      input.style.position = "fixed";
      input.style.left = "-9999px";
      document.body.appendChild(input);
      input.select();
      document.execCommand("copy");
      input.remove();
    }
    setCopyPathCopiedState();
  } catch (error) {
    console.error(error);
  }
}
`,
			1,
		)
	}
	report = strings.Replace(
		report,
		`function setCopyPathCopiedState() {
  if (!el.copyPathButton) return;
  el.copyPathButton.classList.add("copied");
  const originalTitle = el.copyPathButton.title;
  el.copyPathButton.title = "Copied";
  window.setTimeout(() => {
    el.copyPathButton.classList.remove("copied");
    el.copyPathButton.title = originalTitle;
  }, 1200);
}

async function copyCurrentPath() {
  const path = currentPathLabel();
  if (!path) return;
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(path);
    } else {
      const input = document.createElement("textarea");
      input.value = path;
      input.setAttribute("readonly", "");
      input.style.position = "fixed";
      input.style.left = "-9999px";
      document.body.appendChild(input);
      input.select();
      document.execCommand("copy");
      input.remove();
    }
    setCopyPathCopiedState();
  } catch (error) {
    console.error(error);
  }
}
`,
		`const copyMessageTimers = new WeakMap();

function showCopyPathMessage(button, message = "Path copied") {
  if (!button || !button.parentNode) return;
  let messageEl = button.nextElementSibling;
  if (!messageEl || !messageEl.classList.contains("copy-path-message")) {
    messageEl = document.createElement("span");
    messageEl.className = "copy-path-message";
    messageEl.setAttribute("role", "status");
    messageEl.setAttribute("aria-live", "polite");
    button.insertAdjacentElement("afterend", messageEl);
  }
  messageEl.textContent = message;
  messageEl.classList.add("show");
  const existingTimer = copyMessageTimers.get(button);
  if (existingTimer) window.clearTimeout(existingTimer);
  const timer = window.setTimeout(() => {
    messageEl.classList.remove("show");
  }, 1700);
  copyMessageTimers.set(button, timer);
}

function setPathCopyButtonCopied(button) {
  if (!button) return;
  button.classList.add("copied");
  const originalTitle = button.title;
  button.title = "Copied";
  window.setTimeout(() => {
    button.classList.remove("copied");
    button.title = originalTitle;
  }, 1200);
}

async function copyTextToClipboard(text) {
  if (navigator.clipboard && navigator.clipboard.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const input = document.createElement("textarea");
  input.value = text;
  input.setAttribute("readonly", "");
  input.style.position = "fixed";
  input.style.left = "-9999px";
  document.body.appendChild(input);
  input.select();
  document.execCommand("copy");
  input.remove();
}

async function copyPathValue(path, button) {
  if (!path) return;
  try {
    await copyTextToClipboard(path);
    setPathCopyButtonCopied(button);
    showCopyPathMessage(button, "Path copied");
  } catch (error) {
    console.error(error);
  }
}

function makeCopyPathButton(path, label = "Copy path") {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "inline-copy-path-btn";
  button.title = label;
  button.setAttribute("aria-label", label);
  const icon = document.createElement("img");
  icon.className = "inline-copy-path-icon";
  icon.src = "`+copyIconURL+`";
  icon.alt = "";
  icon.setAttribute("aria-hidden", "true");
  button.appendChild(icon);
  button.addEventListener("click", event => {
    event.preventDefault();
    event.stopPropagation();
    copyPathValue(path, button);
  });
  return button;
}

function renderPathWithCopy(container, path, label = "Copy path") {
  container.textContent = "";
  container.title = path;
  const line = document.createElement("span");
  line.className = "path-copy-line";
  const text = document.createElement("span");
  text.className = "path-copy-text";
  text.textContent = path;
  line.append(text, makeCopyPathButton(path, label));
  container.appendChild(line);
}

async function copyCurrentPath() {
  if (state.current === DATA) return;
  copyPathValue(currentPathLabel(), el.copyPathButton);
}
`,
		1,
	)
	report = strings.Replace(
		report,
		`let copyToastTimer = 0;

function showCopyToast(message = "Path copied") {
  let toast = document.querySelector(".copy-toast");
  if (!toast) {
    toast = document.createElement("div");
    toast.className = "copy-toast";
    toast.setAttribute("role", "status");
    toast.setAttribute("aria-live", "polite");
    document.body.appendChild(toast);
  }
  toast.textContent = message;
  toast.classList.add("show");
  if (copyToastTimer) window.clearTimeout(copyToastTimer);
  copyToastTimer = window.setTimeout(() => {
    toast.classList.remove("show");
  }, 1700);
}

function setPathCopyButtonCopied(button) {
  if (!button) return;
  button.classList.add("copied");
  const originalTitle = button.title;
  button.title = "Copied";
  window.setTimeout(() => {
    button.classList.remove("copied");
    button.title = originalTitle;
  }, 1200);
}

async function copyTextToClipboard(text) {
  if (navigator.clipboard && navigator.clipboard.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const input = document.createElement("textarea");
  input.value = text;
  input.setAttribute("readonly", "");
  input.style.position = "fixed";
  input.style.left = "-9999px";
  document.body.appendChild(input);
  input.select();
  document.execCommand("copy");
  input.remove();
}

async function copyPathValue(path, button) {
  if (!path) return;
  try {
    await copyTextToClipboard(path);
    setPathCopyButtonCopied(button);
    showCopyToast("Path copied");
  } catch (error) {
    console.error(error);
  }
}
`,
		`const copyMessageTimers = new WeakMap();

function showCopyPathMessage(button, message = "Path copied") {
  if (!button || !button.parentNode) return;
  let messageEl = button.nextElementSibling;
  if (!messageEl || !messageEl.classList.contains("copy-path-message")) {
    messageEl = document.createElement("span");
    messageEl.className = "copy-path-message";
    messageEl.setAttribute("role", "status");
    messageEl.setAttribute("aria-live", "polite");
    button.insertAdjacentElement("afterend", messageEl);
  }
  messageEl.textContent = message;
  messageEl.classList.add("show");
  const existingTimer = copyMessageTimers.get(button);
  if (existingTimer) window.clearTimeout(existingTimer);
  const timer = window.setTimeout(() => {
    messageEl.classList.remove("show");
  }, 1700);
  copyMessageTimers.set(button, timer);
}

function setPathCopyButtonCopied(button) {
  if (!button) return;
  button.classList.add("copied");
  const originalTitle = button.title;
  button.title = "Copied";
  window.setTimeout(() => {
    button.classList.remove("copied");
    button.title = originalTitle;
  }, 1200);
}

async function copyTextToClipboard(text) {
  if (navigator.clipboard && navigator.clipboard.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const input = document.createElement("textarea");
  input.value = text;
  input.setAttribute("readonly", "");
  input.style.position = "fixed";
  input.style.left = "-9999px";
  document.body.appendChild(input);
  input.select();
  document.execCommand("copy");
  input.remove();
}

async function copyPathValue(path, button) {
  if (!path) return;
  try {
    await copyTextToClipboard(path);
    setPathCopyButtonCopied(button);
    showCopyPathMessage(button, "Path copied");
  } catch (error) {
    console.error(error);
  }
}
`,
		1,
	)
	report = strings.ReplaceAll(
		report,
		`async function copyCurrentPath() {
  copyPathValue(currentPathLabel(), el.copyPathButton);
}`,
		`async function copyCurrentPath() {
  if (state.current === DATA) return;
  copyPathValue(currentPathLabel(), el.copyPathButton);
}`,
	)
	report = strings.Replace(
		report,
		`    path.className = "top-file-path";
    path.textContent = file.path || file.name;`,
		`    path.className = "top-file-path";
    const filePath = file.path || file.name;
    path.title = filePath;
    const pathText = document.createElement("span");
    pathText.className = "path-copy-text";
    pathText.textContent = filePath;
    path.append(pathText, makeCopyPathButton(filePath, "Copy file path"));`,
		1,
	)
	report = strings.Replace(
		report,
		`  el.detailName.textContent = node.name;
  el.detailPath.textContent = node.path || node.name;
  el.detailStats.textContent = "";`,
		`  el.detailName.textContent = node.name;
  renderPathWithCopy(el.detailPath, node.path || node.name, "Copy detail path");
  el.detailStats.textContent = "";`,
		1,
	)
	if !strings.Contains(report, `el.copyPathButton.addEventListener("click", copyCurrentPath);`) {
		report = strings.Replace(
			report,
			`el.helpButton.addEventListener("click", openHelpPage);`,
			`if (el.copyPathButton) el.copyPathButton.addEventListener("click", copyCurrentPath);
el.helpButton.addEventListener("click", openHelpPage);`,
			1,
		)
	}
	if !strings.Contains(report, "compactCrumbs();\n  renderVisibleTreeRows();") {
		report = strings.Replace(
			report,
			`  if (state.current === DATA) syncHomePaneSize();
  renderVisibleTreeRows();`,
			`  if (state.current === DATA) syncHomePaneSize();
  compactCrumbs();
  renderVisibleTreeRows();`,
			1,
		)
	}
	if !strings.Contains(report, "html[data-theme=\"light\"] .copy-path-btn") {
		report = strings.Replace(
			report,
			`html[data-theme="light"] .tree-parent-btn{border-color:#cbd5e1;background:#f8fafc;color:#64748b}`,
			`html[data-theme="light"] .copy-path-btn,html[data-theme="light"] .tree-parent-btn{border-color:#cbd5e1;background:#f8fafc;color:#64748b}`,
			1,
		)
	}
	if !strings.Contains(report, "html[data-theme=\"light\"] .copy-path-btn:hover") {
		report = strings.Replace(
			report,
			`html[data-theme="light"] .tree-parent-btn:hover:not(:disabled){border-color:#93c5fd;color:#0f172a;background:#eaf2fb}`,
			`html[data-theme="light"] .copy-path-btn:hover,html[data-theme="light"] .copy-path-btn.copied,html[data-theme="light"] .tree-parent-btn:hover:not(:disabled){border-color:#93c5fd;color:#0f172a;background:#eaf2fb}`,
			1,
		)
	}
	report = strings.ReplaceAll(report, `ellipsis.textContent = "...";`, `ellipsis.textContent = "....";`)
	report = strings.ReplaceAll(report, `html[data-theme="light"] .copy-path-btn,html[data-theme="light"] .tree-parent-btn{border-color:#cbd5e1;background:#f8fafc;color:#64748b}`, `html[data-theme="light"] .tree-parent-btn{border-color:#cbd5e1;background:#f8fafc;color:#64748b}`)
	report = strings.ReplaceAll(report, `html[data-theme="light"] .copy-path-btn:hover,html[data-theme="light"] .copy-path-btn.copied,html[data-theme="light"] .tree-parent-btn:hover:not(:disabled){border-color:#93c5fd;color:#0f172a;background:#eaf2fb}`, `html[data-theme="light"] .tree-parent-btn:hover:not(:disabled){border-color:#93c5fd;color:#0f172a;background:#eaf2fb}`)
	if !strings.Contains(report, `html[data-theme="light"] .copy-path-icon{filter:none}`) {
		report = strings.Replace(
			report,
			`html[data-theme="light"] .tree-parent-btn{border-color:#cbd5e1;background:#f8fafc;color:#64748b}`,
			`html[data-theme="light"] .copy-path-icon{filter:none}html[data-theme="light"] .tree-parent-btn{border-color:#cbd5e1;background:#f8fafc;color:#64748b}`,
			1,
		)
	}
	if !strings.Contains(report, `html[data-theme="light"] .inline-copy-path-icon{filter:none}`) {
		report = strings.Replace(
			report,
			`html[data-theme="light"] .copy-path-icon{filter:none}`,
			`html[data-theme="light"] .copy-path-icon,html[data-theme="light"] .inline-copy-path-icon{filter:none}`,
			1,
		)
	}
	return report
}

func replaceFolderIcon(report string) string {
	report = strings.Replace(
		report,
		`.row.dir .swatch{width:13px;height:9px;margin-top:3px;border-radius:2px;position:relative}`,
		`.row.dir .swatch{width:18px;height:18px;margin-top:0;border-radius:0;position:relative;background:transparent url("https://img.icons8.com/?size=100&id=Vps0Nsl80v4P&format=png&color=000000") center/contain no-repeat;box-shadow:none}`,
		1,
	)
	report = strings.Replace(
		report,
		`.row.dir .swatch::before{content:"";position:absolute;left:1px;top:-4px;width:7px;height:4px;border-radius:2px 2px 0 0;background:inherit;box-shadow:inset 0 0 0 1px rgba(0,0,0,0.12)}`,
		`.row.dir .swatch::before{content:none}`,
		1,
	)
	return report
}

func replaceReportDataLoader(report string) string {
	if strings.Contains(report, "function base85Decode(") {
		return report
	}
	old := `function unpackReportData(payload) {
  const strings = payload[0] || [];
  const packedRoot = payload[1];

  function valueAt(index) {
    return index >= 0 ? strings[index] : "";
  }

  function joinNodePath(parentPath, name) {
    if (!parentPath) return name;
    if (parentPath === "/") return "/" + name.replace(/^\/+/, "");
    return parentPath.replace(/\/+$/, "") + "/" + name.replace(/^\/+/, "");
  }

  function decodeNode(packed, parentPath) {
    const name = valueAt(packed[0]);
    const path = packed[1] >= 0 ? valueAt(packed[1]) : joinNodePath(parentPath, name);
    const type = packed[3] ? "dir" : "file";
    const node = {
      name,
      path,
      size: packed[2] || 0,
      type,
      ext: valueAt(packed[4]) || (type === "dir" ? "" : "[no extension]"),
      children: []
    };
    const mtime = valueAt(packed[5]);
    const mime = valueAt(packed[6]);
    const flag = valueAt(packed[7]);
    if (mtime) node.mtime = mtime;
    if (mime) node.mime = mime;
    if (flag) node.flag = flag;
    node.children = (Array.isArray(packed[8]) ? packed[8] : []).map(child => decodeNode(child, path));
    return node;
  }

  return decodeNode(packedRoot, "");
}

function bytesFromBase64(value) {
  const binary = atob(value || "");
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

function utf8Bytes(value) {
  return new TextEncoder().encode(value);
}

async function loadCompressedReportData(bytes) {
  if (typeof DecompressionStream !== "function") {
    throw new Error("This browser cannot decompress embedded report data. Use a current Chrome, Edge, Firefox, or Safari release.");
  }
  const stream = new Blob([bytes]).stream().pipeThrough(new DecompressionStream("gzip"));
  const text = await new Response(stream).text();
  return unpackReportData(JSON.parse(text));
}

async function loadReportData(payload) {
  const compressed = payload && payload.encrypted
    ? await decryptReportPayload(payload)
    : bytesFromBase64(payload && payload.payload);
  return loadCompressedReportData(compressed);
}
`
	new := `const BASE85_ALPHABET = "!#$%'()*+,-./0123456789:;=?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[]^_` + "`" + `abcdefghijklmnopqrstuvwxyz{|}~";
const BASE85_DECODE = (() => {
  const table = new Int16Array(128);
  table.fill(-1);
  for (let index = 0; index < BASE85_ALPHABET.length; index++) {
    table[BASE85_ALPHABET.charCodeAt(index)] = index;
  }
  return table;
})();

function base85Decode(value, decodedLength) {
  const text = String(value || "");
  if (text.length % 5 !== 0) throw new Error("Invalid report payload encoding.");
  const fullLength = text.length / 5 * 4;
  const length = Number.isFinite(decodedLength) ? decodedLength : fullLength;
  if (length < 0 || length > fullLength) throw new Error("Invalid report payload length.");
  const bytes = new Uint8Array(fullLength);
  let output = 0;
  for (let offset = 0; offset < text.length; offset += 5) {
    let number = 0;
    for (let i = 0; i < 5; i++) {
      const code = text.charCodeAt(offset + i);
      const digit = code < BASE85_DECODE.length ? BASE85_DECODE[code] : -1;
      if (digit < 0) throw new Error("Invalid report payload character.");
      number = number * 85 + digit;
    }
    bytes[output++] = number >>> 24;
    bytes[output++] = number >>> 16 & 255;
    bytes[output++] = number >>> 8 & 255;
    bytes[output++] = number & 255;
  }
  return bytes.subarray(0, length);
}

function bytesFromBase64(value) {
  const binary = atob(value || "");
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

function bytesFromReportPayload(payload) {
  if (payload && payload.encoding === "base85") {
    return base85Decode(payload.payload, Number(payload.length));
  }
  return bytesFromBase64(payload && payload.payload);
}

function utf8Bytes(value) {
  return new TextEncoder().encode(value);
}

function binaryReportReader(bytes) {
  let offset = 0;
  const decoder = new TextDecoder();
  return {
    readByte() {
      if (offset >= bytes.length) throw new Error("Unexpected end of report data.");
      return bytes[offset++];
    },
    readBytes(length) {
      if (offset + length > bytes.length) throw new Error("Unexpected end of report data.");
      const value = bytes.subarray(offset, offset + length);
      offset += length;
      return value;
    },
    readString() {
      return decoder.decode(this.readBytes(this.readUvarint()));
    },
    readUvarint() {
      let value = 0;
      let shift = 0;
      for (let i = 0; i < 10; i++) {
        const byte = this.readByte();
        value += (byte & 127) * 2 ** shift;
        if (byte < 128) return value;
        shift += 7;
      }
      throw new Error("Invalid report data integer.");
    }
  };
}

function unpackReportData(bytes) {
  const reader = binaryReportReader(bytes);
  if (
    reader.readByte() !== 71 ||
    reader.readByte() !== 68 ||
    reader.readByte() !== 83 ||
    reader.readByte() !== 1
  ) {
    throw new Error("Unsupported report data format.");
  }
  const strings = [];
  const stringCount = reader.readUvarint();
  for (let index = 0; index < stringCount; index++) {
    strings.push(reader.readString());
  }

  function valueAt(encodedIndex) {
    return encodedIndex > 0 ? strings[encodedIndex - 1] : "";
  }

  function joinNodePath(parentPath, name) {
    if (!parentPath) return name;
    if (parentPath === "/") return "/" + name.replace(/^\/+/, "");
    return parentPath.replace(/\/+$/, "") + "/" + name.replace(/^\/+/, "");
  }

  function decodeNode(parentPath) {
    const name = valueAt(reader.readUvarint());
    const pathIndex = reader.readUvarint();
    const path = pathIndex > 0 ? valueAt(pathIndex) : joinNodePath(parentPath, name);
    const size = reader.readUvarint();
    const type = reader.readUvarint() ? "dir" : "file";
    const node = {
      name,
      path,
      size,
      type,
      ext: valueAt(reader.readUvarint()) || (type === "dir" ? "" : "[no extension]"),
      children: []
    };
    const mtime = valueAt(reader.readUvarint());
    const mime = valueAt(reader.readUvarint());
    const flag = valueAt(reader.readUvarint());
    const childCount = reader.readUvarint();
    if (mtime) node.mtime = mtime;
    if (mime) node.mime = mime;
    if (flag) node.flag = flag;
    for (let index = 0; index < childCount; index++) {
      node.children.push(decodeNode(path));
    }
    return node;
  }

  return decodeNode("");
}

async function loadCompressedReportData(bytes) {
  if (typeof DecompressionStream !== "function") {
    throw new Error("This browser cannot decompress embedded report data. Use a current Chrome, Edge, Firefox, or Safari release.");
  }
  const stream = new Blob([bytes]).stream().pipeThrough(new DecompressionStream("gzip"));
  const data = new Uint8Array(await new Response(stream).arrayBuffer());
  return unpackReportData(data);
}

async function loadReportData(payload) {
  const compressed = payload && payload.encrypted
    ? await decryptReportPayload(payload)
    : bytesFromReportPayload(payload);
  return loadCompressedReportData(compressed);
}
`
	return strings.Replace(report, old, new, 1)
}

func replaceSearchIndexBuild(report string) string {
	report = strings.Replace(
		report,
		`function scheduleSearchCandidateIndexBuild() {
  if (searchCandidateIndexBuilt || searchCandidateIndexBuilding) return;
  searchCandidateIndexBuilding = true;
  const build = () => {
    searchCandidateIndex.clear();
    for (let index = 0; index < searchIndex.length; index++) {
      addSearchCandidateIndexEntry(searchIndex[index].searchText, index);
    }
    searchCandidateIndexBuilt = true;
    searchCandidateIndexBuilding = false;
  };
  if (typeof requestIdleCallback === "function") {
    requestIdleCallback(build, { timeout: 2000 });
  } else {
    setTimeout(build, 0);
  }
}
`,
		`function scheduleSearchCandidateIndexBuild() {
  if (searchCandidateIndexBuilt || searchCandidateIndexBuilding) return;
  searchCandidateIndexBuilding = true;
  searchCandidateIndex.clear();
  let index = 0;
  const schedule = callback => {
    if (typeof requestIdleCallback === "function") {
      requestIdleCallback(callback, { timeout: 1000 });
    } else {
      setTimeout(() => callback({ timeRemaining: () => 8 }), 0);
    }
  };
  const buildChunk = deadline => {
    const started = performance.now();
    while (
      index < searchIndex.length &&
      ((deadline && deadline.timeRemaining && deadline.timeRemaining() > 2) || performance.now() - started < 8)
    ) {
      addSearchCandidateIndexEntry(searchIndex[index].searchText, index);
      index++;
    }
    if (index < searchIndex.length) {
      schedule(buildChunk);
      return;
    }
    searchCandidateIndexBuilt = true;
    searchCandidateIndexBuilding = false;
  };
  schedule(buildChunk);
}
`,
		1,
	)
	if !strings.Contains(report, "let searchCandidateIndexBuilt = false;") {
		report = strings.Replace(
			report,
			"let searchResults = [];\nlet searchTimer = 0;",
			"let searchResults = [];\nlet searchTimer = 0;\nlet searchCandidateIndexBuilt = false;\nlet searchCandidateIndexBuilding = false;",
			1,
		)
	}
	if !strings.Contains(report, "const SEARCH_FILE_DISABLE_LIMIT = 1000000;") {
		report = strings.Replace(
			report,
			`const SEARCH_TRIGRAM_SIZE = 3;`,
			`const SEARCH_TRIGRAM_SIZE = 3;
const SEARCH_FILE_DISABLE_LIMIT = 1000000;`,
			1,
		)
	}
	if !strings.Contains(report, "let searchDisabled = false;") {
		report = strings.Replace(
			report,
			`let searchCandidateIndexBuilding = false;
let nextNodeId = 0;`,
			`let searchCandidateIndexBuilding = false;
let searchDisabled = false;
let nextNodeId = 0;`,
			1,
		)
	}
	report = strings.Replace(
		report,
		`  const entryIndex = searchIndex.length;
  searchIndex.push(entry);
  addSearchCandidateIndexEntry(entry.searchText, entryIndex);`,
		`  searchIndex.push(entry);`,
		1,
	)
	report = strings.Replace(
		report,
		`  searchIndex.push(entry);`,
		`  if (!searchDisabled) searchIndex.push(entry);`,
		1,
	)
	if !strings.Contains(report, "function scheduleSearchCandidateIndexBuild()") {
		report = strings.Replace(
			report,
			`function walk(node, parentNode, depth = 0) {`,
			`function scheduleSearchCandidateIndexBuild() {
  if (searchCandidateIndexBuilt || searchCandidateIndexBuilding) return;
  searchCandidateIndexBuilding = true;
  searchCandidateIndex.clear();
  let index = 0;
  const schedule = callback => {
    if (typeof requestIdleCallback === "function") {
      requestIdleCallback(callback, { timeout: 1000 });
    } else {
      setTimeout(() => callback({ timeRemaining: () => 8 }), 0);
    }
  };
  const buildChunk = deadline => {
    const started = performance.now();
    while (
      index < searchIndex.length &&
      ((deadline && deadline.timeRemaining && deadline.timeRemaining() > 2) || performance.now() - started < 8)
    ) {
      addSearchCandidateIndexEntry(searchIndex[index].searchText, index);
      index++;
    }
    if (index < searchIndex.length) {
      schedule(buildChunk);
      return;
    }
    searchCandidateIndexBuilt = true;
    searchCandidateIndexBuilding = false;
  };
  schedule(buildChunk);
}

function walk(node, parentNode, depth = 0) {`,
			1,
		)
	}
	if !strings.Contains(report, "function countReportFiles(root)") {
		report = strings.Replace(
			report,
			`function walk(node, parentNode, depth = 0) {`,
			`function countReportFiles(root) {
  let files = 0;
  const stack = root ? [root] : [];
  while (stack.length) {
    const node = stack.pop();
    if (node.type === "dir") {
      (node.children || []).forEach(child => stack.push(child));
    } else {
      files++;
      if (files > SEARCH_FILE_DISABLE_LIMIT) return files;
    }
  }
  return files;
}

function setSearchAvailability(fileCount) {
  searchDisabled = fileCount > SEARCH_FILE_DISABLE_LIMIT;
  closeSearchResults();
  el.searchInput.disabled = searchDisabled;
  el.searchInput.value = searchDisabled ? "" : el.searchInput.value;
  el.searchInput.placeholder = searchDisabled
    ? "Search disabled for reports over 1,000,000 files"
    : "Search files and folders";
  el.searchInput.setAttribute("aria-disabled", String(searchDisabled));
  if (el.searchShortcut) {
    el.searchShortcut.disabled = searchDisabled;
    el.searchShortcut.setAttribute("aria-disabled", String(searchDisabled));
  }
}

function walk(node, parentNode, depth = 0) {`,
			1,
		)
	}
	report = strings.Replace(
		report,
		`function scheduleSearchCandidateIndexBuild() {
  if (searchCandidateIndexBuilt || searchCandidateIndexBuilding) return;`,
		`function scheduleSearchCandidateIndexBuild() {
  if (searchDisabled || searchCandidateIndexBuilt || searchCandidateIndexBuilding) return;`,
		1,
	)
	report = strings.Replace(
		report,
		`        bytesFromBase64(payload.payload),`,
		`        bytesFromReportPayload(payload),`,
		1,
	)
	report = strings.Replace(
		report,
		`  const results = [];
  const candidateIndexes = candidateIndexesForSearchTerms(terms);`,
		`  if (!searchCandidateIndexBuilt) scheduleSearchCandidateIndexBuild();
  const results = [];
  const candidateIndexes = searchCandidateIndexBuilt ? candidateIndexesForSearchTerms(terms) : null;`,
		1,
	)
	report = strings.Replace(
		report,
		`  if (!searchCandidateIndexBuilt) scheduleSearchCandidateIndexBuild();
  const results = [];`,
		`  if (searchDisabled) return [];
  if (!searchCandidateIndexBuilt) scheduleSearchCandidateIndexBuild();
  const results = [];`,
		1,
	)
	report = strings.Replace(
		report,
		`  searchIndex.length = 0;
  searchCandidateIndex.clear();
  closeSearchResults();`,
		`  searchIndex.length = 0;
  searchCandidateIndex.clear();
  searchCandidateIndexBuilt = false;
  searchCandidateIndexBuilding = false;
  closeSearchResults();`,
		1,
	)
	report = strings.Replace(
		report,
		`  DATA = root;
  walk(DATA, null);
  state.topFiles = buildTopFilesIndex(DATA);`,
		`  DATA = root;
  setSearchAvailability(countReportFiles(DATA));
  walk(DATA, null);
  state.topFiles = buildTopFilesIndex(DATA);`,
		1,
	)
	if !strings.Contains(report, "if (!searchDisabled) scheduleSearchCandidateIndexBuild();") {
		report = strings.Replace(
			report,
			`    renderSafely();
    hideLoadingOverlay();`,
			`    renderSafely();
    hideLoadingOverlay();
    if (!searchDisabled) scheduleSearchCandidateIndexBuild();`,
			1,
		)
	}
	report = strings.Replace(
		report,
		`    renderSafely();
    hideLoadingOverlay();
    scheduleSearchCandidateIndexBuild();`,
		`    renderSafely();
    hideLoadingOverlay();
    if (!searchDisabled) scheduleSearchCandidateIndexBuild();`,
		1,
	)
	report = strings.Replace(
		report,
		`function renderSearchResultsForQuery(query) {
  const normalized = normalizeSearchText(query);`,
		`function renderSearchResultsForQuery(query) {
  if (searchDisabled) {
    closeSearchResults();
    return;
  }
  const normalized = normalizeSearchText(query);`,
		1,
	)
	report = strings.Replace(
		report,
		`function scheduleSearchResults() {
  if (searchTimer) clearTimeout(searchTimer);`,
		`function scheduleSearchResults() {
  if (searchDisabled) return;
  if (searchTimer) clearTimeout(searchTimer);`,
		1,
	)
	report = strings.Replace(
		report,
		`function focusSearch() {
  if (!DATA) return;`,
		`function focusSearch() {
  if (!DATA || searchDisabled) return;`,
		1,
	)
	for strings.Contains(report, "    scheduleSearchCandidateIndexBuild();\n    scheduleSearchCandidateIndexBuild();") {
		report = strings.ReplaceAll(
			report,
			"    scheduleSearchCandidateIndexBuild();\n    scheduleSearchCandidateIndexBuild();",
			"    scheduleSearchCandidateIndexBuild();",
		)
	}
	return report
}

func replacePayload(report, payload string) (string, error) {
	startMarker := "const REPORT_DATA_PAYLOAD = "
	endMarker := ";\nlet DATA = null;"
	start := strings.Index(report, startMarker)
	if start < 0 {
		return "", fmt.Errorf("report template is missing payload marker")
	}
	payloadStart := start + len(startMarker)
	end := strings.Index(report[payloadStart:], endMarker)
	if end < 0 {
		return "", fmt.Errorf("report template is missing payload terminator")
	}
	payloadEnd := payloadStart + end
	var builder strings.Builder
	builder.Grow(len(report) - (payloadEnd - payloadStart) + len(payload))
	builder.WriteString(report[:payloadStart])
	builder.WriteString(payload)
	builder.WriteString(report[payloadEnd:])
	return builder.String(), nil
}

func replaceGeneratedMetadata(report, generatedISO, generatedDisplay string) string {
	title := escapeHTML(appTitle + " - Generated " + generatedDisplay)
	report = generatedTitlePattern.ReplaceAllString(report, "<title>"+title+"</title>")
	report = generatedTimePattern.ReplaceAllString(
		report,
		`<time class="generated" datetime="`+escapeHTML(generatedISO)+`">Generated `+escapeHTML(generatedDisplay)+`</time>`,
	)
	return report
}

func replaceSecurityStatus(report string, encrypted bool) string {
	footerStart := strings.Index(report, `<footer class="footer">`)
	metaStart := strings.Index(report, `<span class="footer-meta">`)
	if footerStart < 0 || metaStart < 0 || metaStart <= footerStart {
		return report
	}
	return report[:footerStart+len(`<footer class="footer">`)] + securityStatusHTML(encrypted) + report[metaStart:]
}

func securityStatusHTML(encrypted bool) string {
	if encrypted {
		return `<span class="report-security encrypted" title="Embedded scan data is encrypted and requires the report password."><svg class="security-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M7 11V8a5 5 0 0 1 10 0v3"/><rect x="5" y="11" width="14" height="9" rx="2"/><path d="M12 15v2"/></svg><span>Data encrypted</span></span>`
	}
	return `<span class="report-security plain" title="Embedded scan data is not encrypted."><svg class="security-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M8 11V8a4 4 0 0 1 7.6-1.8"/><rect x="5" y="11" width="14" height="9" rx="2"/><path d="M12 15v2"/></svg><span>Data not encrypted</span></span>`
}

func fillReportSize(report string) string {
	sizeLabel := formatReportFileSize(len([]byte(report)))
	for i := 0; i < 10; i++ {
		candidate := reportSizePattern.ReplaceAllString(report, sizeLabel)
		nextLabel := formatReportFileSize(len([]byte(candidate)))
		if nextLabel == sizeLabel {
			return candidate
		}
		sizeLabel = nextLabel
	}
	return reportSizePattern.ReplaceAllString(report, sizeLabel)
}

func formatReportFileSize(size int) string {
	units := []string{"bytes", "KiB", "MiB", "GiB"}
	value := float64(size)
	unit := units[0]
	for _, candidate := range units {
		unit = candidate
		if value < 1024 || unit == units[len(units)-1] {
			break
		}
		value /= 1024
	}
	var amount string
	switch {
	case unit == "bytes":
		amount = commaInt(size)
	case value >= 100:
		amount = fmt.Sprintf("%.0f", value)
	case value >= 10:
		amount = fmt.Sprintf("%.1f", value)
	default:
		amount = fmt.Sprintf("%.2f", value)
	}
	return "HTML file: " + amount + " " + unit
}

func commaInt(value int) string {
	text := fmt.Sprintf("%d", value)
	if len(text) <= 3 {
		return text
	}
	var out []byte
	prefix := len(text) % 3
	if prefix == 0 {
		prefix = 3
	}
	out = append(out, text[:prefix]...)
	for i := prefix; i < len(text); i += 3 {
		out = append(out, ',')
		out = append(out, text[i:i+3]...)
	}
	return string(out)
}

func escapeHTML(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&#34;",
		"'", "&#39;",
	)
	return replacer.Replace(value)
}
