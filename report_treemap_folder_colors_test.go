package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestTreemapFolderColorsUseStableDistinctSiblingColors(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is unavailable; treemap folder color checks require Node.js")
	}
	report, err := renderReport(&Node{Name: "root", Path: "/data/root", Type: "dir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	colorSource, err := reportFunctionRegion(report, "function hashString(", "function hashForNode(")
	if err != nil {
		t.Fatal(err)
	}
	itemsSource, err := reportFunctionRegion(report, "function effectiveTreemapVisibleEntryLimit(", "function hasTreemapChildren(")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]string{"colors": colorSource, "items": itemsSource})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "-e", treemapFolderColorsNodeTest)
	cmd.Stdin = strings.NewReader(string(payload))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("runtime treemap folder color checks failed: %v\n%s", err, output)
	}
}

const treemapFolderColorsNodeTest = `
const assert = require("node:assert/strict");
const fs = require("node:fs");
const source = JSON.parse(fs.readFileSync(0, "utf8"));
const DEFAULT_TREEMAP_TILE_CAP = 100;
const TREEMAP_TILE_CAP_OPTIONS = [5, 10, 20, 50, 100];
const TREEMAP_SMALLER_ENTRIES_COLOR = "#475569";
const state = {treemapTileCap: 100, current: null};
let DATA = null;
function nodeParent(node) { return node ? node.parentNode || null : null; }
function formatCount(value) { return String(value); }
function colorFor() { return "#000000"; }
eval(source.colors + "\n" + source.items);

function makeTree() {
  const makeDir = (name, path, size, children = []) => ({name, path, size, type: "dir", id: path, children, parentNode: null});
  const deep = makeDir("deep", "/data/alpha/shared/deep", 8);
  const sharedA = makeDir("shared", "/data/alpha/shared", 10, [deep]);
  const sharedB = makeDir("shared", "/data/beta/shared", 10);
  const alpha = makeDir("alpha", "/data/alpha", 30, [sharedA]);
  const beta = makeDir("beta", "/data/beta", 30, [sharedB]);
  const gamma = makeDir("gamma", "/data/gamma", 30);
  const root = makeDir("data", "/data", 100, [alpha, beta, gamma]);
  for (const parent of [root, alpha, beta, sharedA]) {
    for (const child of parent.children) child.parentNode = parent;
  }
  return {root, alpha, beta, gamma, sharedA, sharedB, deep};
}

let tree = makeTree();
DATA = tree.root;
const colors = node => directoryTreemapColorFor(node);
const original = new Map([tree.alpha, tree.beta, tree.gamma, tree.sharedA, tree.sharedB, tree.deep].map(node => [node.path, colors(node)]));
assert.equal(new Set([tree.alpha, tree.beta, tree.gamma].map(colors)).size, 3,
  "visible sibling directories should use distinct palette colors when available");
assert.notEqual(colors(tree.sharedA), colors(tree.alpha), "child directory should contrast with its parent");
assert.notEqual(colors(tree.sharedB), colors(tree.beta), "same-named child should contrast with its own parent");
assert.notEqual(colors(tree.deep), colors(tree.sharedA), "deeper child should contrast with its immediate parent");

state.current = tree.beta;
assert.equal(colors(tree.alpha), original.get(tree.alpha.path), "navigation must not recolor an existing folder");
state.current = tree.sharedA;
assert.equal(colors(tree.deep), original.get(tree.deep.path), "nested navigation must not recolor a folder");

tree = makeTree();
DATA = tree.root;
for (const node of [tree.alpha, tree.beta, tree.gamma, tree.sharedA, tree.sharedB, tree.deep]) {
  assert.equal(colors(node), original.get(node.path), "reload must preserve full-path color for " + node.path);
}

function makeCohortTree() {
  const children = Array.from({length: 14}, (_, index) => ({
    name: "folder-" + String(index).padStart(2, "0"),
    path: "/cohort/folder-" + String(index).padStart(2, "0"),
    size: 140 - index * 3,
    type: "dir",
    id: "cohort-" + index,
    children: [],
    parentNode: null
  }));
  const root = {name: "cohort", path: "/cohort", size: 1000, type: "dir", id: "cohort", children, parentNode: null};
  for (const child of children) child.parentNode = root;
  return {root, children};
}

const palette = ["#2563EB", "#4F46E5", "#7C3AED", "#A21CAF", "#DB2777", "#E11D48",
  "#EA580C", "#D97706", "#65A30D", "#16A34A", "#0D9488", "#0891B2"];
function expectedPathHash(value) {
  let hash = 0;
  for (let index = 0; index < value.length; index++) hash = ((hash << 5) - hash + value.charCodeAt(index)) | 0;
  return Math.abs(hash);
}
function expectedCohortColors(root, children) {
  const parentIndex = palette.indexOf(directoryTreemapColorFor(root));
  const used = new Set();
  const expected = new Map();
  const sorted = children.slice().sort((left, right) => left.path < right.path ? -1 : left.path > right.path ? 1 : 0);
  for (const child of sorted) {
    const preferred = expectedPathHash(child.path) % palette.length;
    let chosen = -1;
    for (let offset = 0; offset < palette.length; offset++) {
      const candidate = (preferred + offset) % palette.length;
      if (candidate !== parentIndex && !used.has(candidate)) { chosen = candidate; break; }
    }
    if (chosen < 0) {
      for (let offset = 0; offset < palette.length; offset++) {
        const candidate = (preferred + offset) % palette.length;
        if (candidate !== parentIndex) { chosen = candidate; break; }
      }
    }
    expected.set(child.path, palette[chosen]);
    used.add(chosen);
  }
  return expected;
}

let cohort = makeCohortTree();
DATA = cohort.root;
state.treemapTileCap = 5;
const capFiveVisible = treemapItems(cohort.root, state.treemapTileCap).filter(node => node.type === "dir");
const capFiveColors = new Map(cohort.children.map(node => [node.path, colors(node)]));
assert.equal(capFiveVisible.length, 5, "cap-five fixture exposes five sibling directories");
assert.equal(new Set(capFiveVisible.map(colors)).size, capFiveVisible.length,
  "visible siblings should be distinct before palette saturation");
assert.ok(cohort.children.every(node => colors(node) !== colors(cohort.root)), "every sibling must avoid its parent color");
assert.deepEqual(capFiveColors, expectedCohortColors(cohort.root, cohort.children),
  "each sibling's full-path hash must seed deterministic lexical collision resolution");

state.treemapTileCap = 100;
for (const node of cohort.children) {
  assert.equal(colors(node), capFiveColors.get(node.path), "changing cap must preserve color for " + node.path);
}
assert.equal(new Set(cohort.children.map(colors)).size, palette.length - 1,
  "the fixed cohort should use every available non-parent color before deterministic reuse");

const transitioned = makeCohortTree();
DATA = transitioned.root;
state.treemapTileCap = 5;
const transitionColors = new Map(transitioned.children.map(node => [node.path, colors(node)]));
state.treemapTileCap = 100;
for (const node of transitioned.children) {
  assert.equal(colors(node), transitionColors.get(node.path), "cap transition must not recolor " + node.path);
}

const freshAtMax = makeCohortTree();
DATA = freshAtMax.root;
state.treemapTileCap = 100;
for (const node of freshAtMax.children) {
  assert.equal(colors(node), capFiveColors.get(node.path), "fresh max-cap load must preserve color for " + node.path);
}

assert.equal(treemapColorFor({type: "file", ext: ".jpg", mime: "image/jpeg"}), "#0EA5E9",
  "file MIME colors should remain unchanged");
assert.equal(treemapColorFor({type: "file", ext: "[other]"}), TREEMAP_SMALLER_ENTRIES_COLOR,
  "smaller-entry aggregate color should remain unchanged");
`
