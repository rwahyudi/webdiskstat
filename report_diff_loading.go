package main

import (
	"fmt"
	"strings"
)

// diffLoadingIntegration reuses the ordinary report's live loader markup and
// style rules so both report types keep one visual source of truth.
func diffLoadingIntegration() (string, string, error) {
	template := optimizedReportTemplate()
	const overlayStart = `<div id="loadingOverlay" class="loading-overlay"`
	start := strings.Index(template, overlayStart)
	if start < 0 {
		return "", "", fmt.Errorf("diff report missing shared loading overlay")
	}
	markup, err := balancedLoadingOverlay(template[start:])
	if err != nil {
		return "", "", fmt.Errorf("diff report missing shared loading overlay end")
	}
	markup = strings.Replace(markup, "Loading report data", "Opening comparison", 1)
	markup = strings.Replace(markup, "Preparing disk usage view", "Preparing comparison view", 1)
	styleStart, styleEnd := strings.Index(template, "<style>"), strings.Index(template, "</style>")
	if styleStart < 0 || styleEnd < styleStart {
		return "", "", fmt.Errorf("diff report missing shared loading styles")
	}
	style := template[styleStart+len("<style>") : styleEnd]
	css, err := loadingCSSRules(style)
	if err != nil {
		return "", "", err
	}
	if !strings.Contains(css, ".loading-overlay") || !strings.Contains(css, ".loading-spinner") || !strings.Contains(css, "@keyframes loading-spin") {
		return "", "", fmt.Errorf("diff report missing shared loading animation rules")
	}
	return markup, css, nil
}

// Extract the balanced overlay root rather than relying on a fixed closing-tag
// sequence: its task list introduces additional nested elements over time.
func balancedLoadingOverlay(source string) (string, error) {
	depth := 0
	for i := 0; i < len(source); {
		if source[i] != '<' {
			i++
			continue
		}
		end := strings.IndexByte(source[i:], '>')
		if end < 0 {
			return "", fmt.Errorf("unterminated loading markup tag")
		}
		tag := source[i : i+end+1]
		if strings.HasPrefix(tag, "</div") {
			depth--
		} else if strings.HasPrefix(tag, "<div") && !strings.HasSuffix(strings.TrimSpace(tag), "/>") {
			depth++
		}
		i += end + 1
		if depth == 0 {
			return source[:i], nil
		}
	}
	return "", fmt.Errorf("unbalanced loading overlay markup")
}

// Parse CSS blocks recursively so media wrappers remain intact and nested
// keyframe selectors are not mistaken for ordinary selector rules.
func loadingCSSRules(source string) (string, error) {
	var output strings.Builder
	for i := 0; i < len(source); {
		open := strings.IndexByte(source[i:], '{')
		if open < 0 {
			break
		}
		open += i
		header := strings.TrimSpace(source[i:open])
		depth, close := 1, open+1
		for ; close < len(source) && depth > 0; close++ {
			if source[close] == '{' {
				depth++
			}
			if source[close] == '}' {
				depth--
			}
		}
		if depth != 0 {
			return "", fmt.Errorf("unbalanced loading CSS block %q", header)
		}
		body := source[open+1 : close-1]
		keep := loadingSelector(header)
		if strings.HasPrefix(header, "@keyframes loading-") || strings.HasPrefix(header, "@-webkit-keyframes loading-") {
			keep = true
		} else if strings.HasPrefix(header, "@media") {
			nested, err := loadingCSSRules(body)
			if err != nil {
				return "", err
			}
			if nested != "" {
				output.WriteString(header + "{" + nested + "}\n")
			}
			i = close
			continue
		}
		if keep {
			output.WriteString(header + "{" + body + "}\n")
		}
		i = close
	}
	return output.String(), nil
}

func loadingSelector(selector string) bool {
	for _, part := range strings.Split(selector, ",") {
		part = strings.TrimSpace(part)
		for _, class := range []string{".loading-overlay", ".loading-panel", ".loading-spinner", ".loading-title", ".loading-subtitle", ".loading-progress", ".loading-task"} {
			if strings.Contains(part, class) {
				return true
			}
		}
	}
	return false
}

const diffLoadingRuntime = `
const loadingSteps = [
  { key: "payload", label: "Decode comparison data" },
  { key: "decrypt", label: "Decrypt comparison data" },
  { key: "decompress", label: "Decompress comparison data" },
  { key: "index", label: "Build directory index" },
  { key: "render", label: "Render comparison" }
];
const loadingStepOrder = new Map(loadingSteps.map((step, index) => [step.key, index]));
function buildLoadingTaskList() {
  const list = $("loadingTasks");
  if (!list) return;
  list.replaceChildren();
  loadingSteps.forEach(step => {
    const item = document.createElement("li");
    item.className = "loading-task";
    item.dataset.step = step.key;
    const dot = document.createElement("span");
    dot.className = "loading-task-dot";
    dot.setAttribute("aria-hidden", "true");
    const label = document.createElement("span");
    label.textContent = step.label;
    item.append(dot, label);
    list.append(item);
  });
}
function setLoadingStep(key, status = "active") {
  const position = loadingStepOrder.get(key);
  if (position === undefined) return;
  document.querySelectorAll("#loadingTasks .loading-task").forEach(item => {
    const itemPosition = loadingStepOrder.get(item.dataset.step);
    item.classList.remove("active", "done", "skipped", "error");
    if (itemPosition < position) item.classList.add("done");
    if (item.dataset.step === key) item.classList.add(status === "error" ? "error" : status);
  });
  const fill = $("loadingProgressFill");
  if (fill) fill.style.width = (position / Math.max(1, loadingSteps.length - 1) * 100) + "%";
  const subtitle = $("loadingSubtitle");
  if (subtitle) subtitle.textContent = status === "error" ? "Could not open comparison" : "Step " + (position + 1) + " of " + loadingSteps.length + ": " + loadingSteps[position].label;
}
function hideLoadingOverlay() {
  const overlay = $("loadingOverlay");
  if (overlay) overlay.hidden = true;
}
function loadingFrame() {
  return new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
}
`
