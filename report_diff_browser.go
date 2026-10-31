package main

import (
	_ "embed"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

//go:embed assets/diff-browser.js
var diffBrowserRuntime string

// Extract complete, top-level declarations from the optimized ordinary report.
// Its functions use unindented outer closing braces; nested closures are indented.
// Missing declarations fail generation instead of silently shipping a stale fork.
func normalBrowserFunction(source, name string) (string, error) {
	start := regexp.MustCompile(`(?m)^(?:async )?function ` + regexp.QuoteMeta(name) + `\(`).FindStringIndex(source)
	if start == nil {
		return "", fmt.Errorf("shared browser function missing: %s", name)
	}
	end := strings.Index(source[start[0]:], "\n}")
	if end < 0 {
		return "", fmt.Errorf("shared browser function incomplete: %s", name)
	}
	return source[start[0]:start[0]+end+2] + "\n", nil
}

var diffBrowserSupportOnce sync.Once
var sharedDiffBrowserJS, sharedDiffBrowserCSS string
var sharedDiffBrowserError error

func diffBrowserSupport() (string, string, error) {
	diffBrowserSupportOnce.Do(func() {
		sharedDiffBrowserJS, sharedDiffBrowserCSS, sharedDiffBrowserError = prepareDiffBrowserSupport()
	})
	return sharedDiffBrowserJS, sharedDiffBrowserCSS, sharedDiffBrowserError
}

// Icon renderers differ across ordinary-report revisions. Extract only the
// constants referenced by the renderer actually being shared. In particular,
// the committed legacy renderer needs neither the newer generic SVG fallback
// nor its MIME lookup maps; requiring those would couple this feature to
// unrelated, uncommitted ordinary-report refinements.
func normalBrowserIconSupport(source string) (string, error) {
	var functions strings.Builder
	for _, name := range []string{"materialIconLookupName", "materialIconSVG", "makeFileIcon"} {
		fn, err := normalBrowserFunction(source, name)
		if err != nil {
			return "", err
		}
		functions.WriteString(fn)
	}
	var declarations strings.Builder
	for _, dependency := range []struct{ name, pattern string }{
		{"MIME_ICON_FILENAMES", `(?s)const MIME_ICON_FILENAMES = new Map\(\[.*?\]\);`},
		{"MIME_FAMILY_ICON_FILENAMES", `(?s)const MIME_FAMILY_ICON_FILENAMES = new Map\(\[.*?\]\);`},
		{"GENERIC_FILE_ICON", `const GENERIC_FILE_ICON = [^\n]+`},
	} {
		if !regexp.MustCompile(`\b` + dependency.name + `\b`).MatchString(functions.String()) {
			continue
		}
		declaration := regexp.MustCompile(dependency.pattern).FindString(source)
		if declaration == "" {
			return "", fmt.Errorf("shared icon renderer requires missing constant: %s", dependency.name)
		}
		declarations.WriteString(declaration + "\n")
	}
	return declarations.String(), nil
}

func prepareDiffBrowserSupport() (string, string, error) {
	source := optimizedReportTemplate()
	var js strings.Builder
	lib := regexp.MustCompile(`(?s)<script>\s*(const MaterialFileIcons = .*?)</script>`).FindStringSubmatch(source)
	if len(lib) != 2 {
		return "", "", fmt.Errorf("shared browser icons missing")
	}
	js.WriteString(lib[1] + "\n")
	for _, name := range []string{"SORT_SHORTCUTS"} {
		value := regexp.MustCompile(`(?s)const ` + name + ` = new Map\(\[.*?\]\);`).FindString(source)
		if value == "" {
			return "", "", fmt.Errorf("shared browser constant missing: %s", name)
		}
		js.WriteString(value + "\n")
	}
	iconSupport, err := normalBrowserIconSupport(source)
	if err != nil {
		return "", "", err
	}
	js.WriteString(iconSupport)
	sorted, err := normalBrowserFunction(source, "sortedChildren")
	if err != nil {
		return "", "", err
	}
	js.WriteString(strings.Replace(sorted, "function sortedChildren(", "function normalSortedChildren(", 1))
	names := strings.Fields(`formatBytes formatCount dateFromModifiedValue formatModifiedTime formatListModifiedTime modifiedSortValue normalizeSearchText
		pathForNode hashForNode currentUrlWithoutHash nodeFromLocationHash syncUrlToCurrent applyLocationHash directoryForNode
		sortIndicator setSort handleSortShortcut currentTreeChildren ensureListSelection
		defaultTreeColumns readStoredTreeColumns storeTreeColumns readStoredTreeColumnWidths storeTreeColumnWidths treeColumnWidth
		isTreeColumnVisible visibleTreeColumns applyTreeColumnLayout closeTreeColumnsMenu reopenTreeColumnsMenu setTreeColumnVisible
		setTreeColumnWidth commitTreeColumnWidth beginTreeColumnResize handleTreeColumnResizeKey makeTreeColumnResizer makeTreeHeaderCell
		makeTreeColumnsButton makeTreeColumnsMenu makeHeaderButton makeParentHeaderButton makeHeaderLabel renderTreeHeader resetTreeView renderVisibleTreeRows
		materialIconLookupName materialIconSVG makeFileIcon makeColumnsIcon makeUpIcon makeHomeIcon
		pathToRoot currentPathLabel makeCrumbSeparator hideCrumbAt compactCrumbs renderCrumbs
		searchValues searchValuesMatch searchScore insertSearchResult findSearchMatches closeSearchResults updateSearchActiveResult
		renderSearchResultsForQuery scheduleSearchResults activateSearchResult moveSearchActiveResult focusSearch selectSearchInputText
		isTextEditingTarget scrollTreeSelectionIntoView focusFirstTreeRow setListSelectionByIndex moveListSelection treePageRowCount openSelectedDirectory handleListKey`)
	for _, name := range names {
		fn, err := normalBrowserFunction(source, name)
		if err != nil {
			return "", "", err
		}
		if name == "activateSearchResult" {
			fn = strings.Replace(fn, "function activateSearchResult(", "function normalActivateSearchResult(", 1)
		}
		js.WriteString(fn)
	}
	style := regexp.MustCompile(`(?s)<style>(.*?)</style>`).FindStringSubmatch(source)
	if len(style) != 2 {
		return "", "", fmt.Errorf("shared browser styles missing")
	}
	return js.String(), scopeDiffBrowserCSS(style[1]), nil
}

// Preserve media conditions and rule order while isolating incumbent browser
// selectors from the comparison summary and independent rankings tables.
func scopeDiffBrowserCSS(css string) string {
	var out strings.Builder
	for len(css) > 0 {
		i := strings.Index(css, "{")
		if i < 0 {
			break
		}
		head := strings.TrimSpace(css[:i])
		depth, end := 1, i+1
		var quote byte
		for ; end < len(css) && depth > 0; end++ {
			c := css[end]
			if quote != 0 {
				if c == '\\' {
					end++
				} else if c == quote {
					quote = 0
				}
				continue
			}
			if c == '"' || c == '\'' {
				quote = c
				continue
			}
			if c == '{' {
				depth++
			}
			if c == '}' {
				depth--
			}
		}
		if depth != 0 {
			break
		}
		body := css[i+1 : end-1]
		css = css[end:]
		if strings.HasPrefix(head, "@media") || strings.HasPrefix(head, "@supports") {
			scoped := scopeDiffBrowserCSS(body)
			if scoped != "" {
				out.WriteString(head + "{" + scoped + "}\n")
			}
			continue
		}
		if strings.HasPrefix(head, "@") {
			continue
		}
		var selectors []string
		for _, selector := range strings.Split(head, ",") {
			selector = strings.TrimSpace(selector)
			keep := false
			for _, part := range []string{".tree", ".row", ".swatch", ".material-file-icon", ".search", ".pathbar", ".crumb", ".current-location", ".copy-path", ".inline-copy", ".sep", ".icon"} {
				if strings.Contains(selector, part) {
					keep = true
					break
				}
			}
			if !keep {
				continue
			}
			if strings.HasPrefix(selector, `html[data-theme=`) {
				space := strings.Index(selector, " ")
				if space < 0 {
					continue
				}
				selector = selector[:space] + " #diffBrowser " + selector[space+1:]
			} else {
				selector = "#diffBrowser " + selector
			}
			selectors = append(selectors, selector)
		}
		if len(selectors) > 0 {
			out.WriteString(strings.Join(selectors, ",") + "{" + body + "}\n")
		}
	}
	return out.String()
}
