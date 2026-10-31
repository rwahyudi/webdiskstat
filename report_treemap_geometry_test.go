package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestTreemapLayoutRuntimeGeometry(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is unavailable; treemap runtime geometry checks require Node.js")
	}
	report, err := renderReport(&Node{Name: "root", Path: "/data/root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	layoutSource, err := reportFunctionRegion(report, "function layoutTreemap(", "function topLevelDirectoryTileRect(")
	if err != nil {
		t.Fatal(err)
	}
	itemsSource, err := reportFunctionRegion(report, "function effectiveTreemapVisibleEntryLimit(", "function hasTreemapChildren(")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]string{"layout": layoutSource, "items": itemsSource})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "-e", treemapGeometryNodeTest)
	cmd.Stdin = strings.NewReader(string(payload))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("runtime treemap geometry checks failed: %v\n%s", err, output)
	}
}

func reportFunctionRegion(report, startMarker, endMarker string) (string, error) {
	start := strings.Index(report, startMarker)
	if start < 0 {
		return "", errMissingReportFunction(startMarker)
	}
	endRelative := strings.Index(report[start:], endMarker)
	if endRelative < 0 {
		return "", errMissingReportFunction(endMarker)
	}
	return report[start : start+endRelative], nil
}

func errMissingReportFunction(marker string) error {
	return &missingReportFunctionError{marker: marker}
}

type missingReportFunctionError struct{ marker string }

func (e *missingReportFunctionError) Error() string {
	return "generated report is missing function boundary " + e.marker
}

const treemapGeometryNodeTest = `
const assert = require("node:assert/strict");
const fs = require("node:fs");
const source = JSON.parse(fs.readFileSync(0, "utf8"));
const DEFAULT_TREEMAP_TILE_CAP = 100;
function formatCount(value) { return String(value); }
eval(source.layout + "\n" + source.items);

const cases = [
  {name: "wide equal", items: [1, 1, 1, 1], x: 13, y: 29, w: 1200, h: 300, squares: true},
  {name: "portrait equal", items: [1, 1, 1, 1], x: 7, y: 11, w: 300, h: 1200, squares: true},
  {name: "square equal", items: [1, 1, 1, 1], x: 31, y: 17, w: 600, h: 600, squares: true},
  {name: "mixed", items: [6, 6, 4, 3, 2, 2, 1], x: 19, y: 23, w: 817, h: 463},
  {name: "dominant", items: [97, 1, 1, 1], x: 3, y: 5, w: 1200, h: 300},
  {name: "single", items: [9], x: 40, y: 50, w: 77, h: 133},
  {name: "tiny", items: [5, 3, 2], x: 0.25, y: 0.75, w: 1e-8, h: 3e-8},
  {name: "extreme wide", items: [4, 3, 2, 1], x: 8, y: 9, w: 10000, h: 10},
  {name: "extreme tall", items: [4, 3, 2, 1], x: 8, y: 9, w: 10, h: 10000}
];

function near(a, b, tolerance) { return Math.abs(a - b) <= tolerance; }
function validate(name, items, x, y, w, h) {
  const input = items.map(size => ({size}));
  const before = JSON.stringify(input);
  const rects = layoutTreemap(input, x, y, w, h);
  assert.equal(rects.length, input.length, name + ": tile count");
  assert.equal(JSON.stringify(input), before, name + ": input was mutated");
  const total = items.reduce((sum, size) => sum + size, 0);
  const containerArea = w * h;
  let areaSum = 0;
  for (let i = 0; i < rects.length; i++) {
    const r = rects[i];
    for (const key of ["x", "y", "w", "h"]) {
      assert.ok(Number.isFinite(r[key]), name + ": non-finite " + key);
    }
    assert.ok(r.w >= 0 && r.h >= 0, name + ": negative dimensions");
    const eps = Math.max(1e-7, Math.max(w, h) * 1e-10);
    assert.ok(r.x >= x - eps && r.y >= y - eps, name + ": outside leading bounds");
    assert.ok(r.x + r.w <= x + w + eps && r.y + r.h <= y + h + eps, name + ": outside trailing bounds");
    const actualArea = r.w * r.h;
    const expectedArea = items[i] / total * containerArea;
    assert.ok(near(actualArea, expectedArea, Math.max(1e-7, expectedArea * 1e-8)),
      name + ": area mismatch for tile " + i + " actual=" + actualArea + " expected=" + expectedArea);
    areaSum += actualArea;
    for (let j = 0; j < i; j++) {
      const other = rects[j];
      const overlapW = Math.min(r.x + r.w, other.x + other.w) - Math.max(r.x, other.x);
      const overlapH = Math.min(r.y + r.h, other.y + other.h) - Math.max(r.y, other.y);
      assert.ok(overlapW <= eps || overlapH <= eps, name + ": tiles " + j + " and " + i + " overlap");
    }
  }
  assert.ok(near(areaSum, containerArea, Math.max(1e-7, containerArea * 1e-8)), name + ": container area not conserved");
  const second = layoutTreemap(input, x, y, w, h);
  assert.deepEqual(second, rects, name + ": layout is not deterministic");
  return rects;
}

for (const c of cases) {
  const rects = validate(c.name, c.items, c.x, c.y, c.w, c.h);
  if (c.squares) {
    for (const r of rects) {
      const ratio = Math.max(r.w / r.h, r.h / r.w);
      assert.ok(ratio <= 1.05, c.name + ": expected square-like tiles, got aspect ratio " + ratio);
    }
  }
}
assert.deepEqual(layoutTreemap([], 0, 0, 20, 10), [], "empty input");
assert.deepEqual(layoutTreemap([{size: 0}, {size: 0}], 0, 0, 20, 10), [], "all-zero input");
assert.deepEqual(layoutTreemap([{size: 1}], 4, 5, 0, 10), [], "zero width");
assert.deepEqual(layoutTreemap([{size: 1}], 4, 5, 10, 0), [], "zero height");

// Exercise recursion's core geometry contract on child rectangles too.
validate("nested child", [8, 5, 3, 2], 0, 0, 316, 174);

const children = Array.from({length: 20}, (_, i) => ({id: "i" + i, name: "entry " + i, size: 1, type: "file"}));
const result = treemapItems({id: "root", name: "root", path: "/root", type: "dir", children}, 5);
assert.equal(result.length, 6, "aggregate plus five visible entries");
assert.equal(result[0].name, "15 smaller entries", "aggregate must be sorted with visible entries, not appended last");
assert.equal(result[0].size, 15, "hidden aggregate size");
assert.ok(result.slice(1).every(item => item.size === 1), "visible entry sizes");
assert.equal(result.reduce((sum, item) => sum + item.size, 0), 20, "aggregate size conservation");
assert.ok(result.every((item, i) => i === 0 || result[i - 1].size >= item.size), "final treemap items must be size-sorted");
`
