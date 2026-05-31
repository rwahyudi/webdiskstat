package main

import (
	"encoding/json"
	"testing"
)

func TestNormalizeGDUObject(t *testing.T) {
	raw := map[string]any{
		"name": "root",
		"children": []any{
			map[string]any{"name": "a.txt", "size": json.Number("10")},
			map[string]any{"name": "dir", "children": []any{
				map[string]any{"name": "b.log", "size": json.Number("5")},
			}},
		},
	}
	root, err := normalizeExport(raw, "gdu")
	if err != nil {
		t.Fatal(err)
	}
	if root.Name != "root" || root.Type != "dir" || root.Size != 15 {
		t.Fatalf("unexpected root: %#v", root)
	}
	if root.Items != 3 || root.Files != 2 {
		t.Fatalf("unexpected totals: items=%d files=%d", root.Items, root.Files)
	}
	if root.Children[0].Name != "a.txt" || root.Children[0].Ext != ".txt" {
		t.Fatalf("children not normalized or sorted as expected: %#v", root.Children)
	}
	if root.Children[1].Children[0].Path != "root/dir/b.log" {
		t.Fatalf("unexpected nested path: %s", root.Children[1].Children[0].Path)
	}
}

func TestNormalizeNCDUExport(t *testing.T) {
	raw := []any{
		json.Number("1"),
		json.Number("2"),
		map[string]any{},
		[]any{
			map[string]any{"name": "/", "asize": json.Number("1")},
			[]any{
				map[string]any{"name": "file.bin", "asize": json.Number("7")},
			},
		},
	}
	root, err := normalizeExport(raw, "ncdu")
	if err != nil {
		t.Fatal(err)
	}
	if root.Name != "/" || root.Size != 8 || root.Files != 1 {
		t.Fatalf("unexpected ncdu root: %#v", root)
	}
}

func TestNormalizePreservesExplicitMIME(t *testing.T) {
	raw := map[string]any{
		"name":        "download",
		"size":        json.Number("12"),
		"contentType": "application/pdf; charset=binary",
	}
	root, err := normalizeExport(raw, "gdu")
	if err != nil {
		t.Fatal(err)
	}
	if root.MIME != "application/pdf" {
		t.Fatalf("MIME = %q, want application/pdf", root.MIME)
	}
}

func TestExtensionFor(t *testing.T) {
	cases := map[string]string{
		"file.TXT":  ".txt",
		".profile":  "[no extension]",
		"Makefile":  "[no extension]",
		"archive.":  "[no extension]",
		"a/b/c.tar": ".tar",
	}
	for input, want := range cases {
		if got := extensionFor(input); got != want {
			t.Fatalf("extensionFor(%q) = %q, want %q", input, got, want)
		}
	}
}
