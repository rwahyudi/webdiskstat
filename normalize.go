package main

import (
	"encoding/json"
	"fmt"
	"mime"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	childKeys = []string{"items", "Items", "children", "Children", "entries", "Entries", "files", "Files", "dirs", "Dirs", "nodes", "Nodes"}
	nameKeys  = []string{"name", "Name", "path", "Path", "fullPath", "FullPath"}
	pathKeys  = []string{"path", "Path", "fullPath", "FullPath"}
	sizeKeys  = []string{"usage", "Usage", "size", "Size", "diskUsage", "DiskUsage", "disk_usage", "dsize", "Dsize", "asize", "Asize", "blocks", "Blocks", "apparentSize", "ApparentSize", "apparent_size", "total", "Total"}
	dirKeys   = []string{"isDir", "IsDir", "dir", "Dir", "directory", "Directory"}
	mtimeKeys = []string{"mtime", "Mtime", "modTime", "ModTime", "modified", "Modified"}
	flagKeys  = []string{"flag", "Flag", "flags", "Flags"}
	mimeKeys  = []string{"mime", "MIME", "mimeType", "MimeType", "contentType", "ContentType"}

	numberPattern = regexp.MustCompile(`^\d+(\.\d+)?$`)
)

func normalizeExport(raw any, inputType string) (*Node, error) {
	if inputType != "gdu" && inputType != "ncdu" {
		return nil, fmt.Errorf("unsupported input type: %s", inputType)
	}
	if isExportArray(raw) {
		if inputType == "ncdu" {
			if err := validateNcduExport(raw.([]any)); err != nil {
				return nil, err
			}
		}
		raw = raw.([]any)[3]
	} else if inputType == "ncdu" {
		return nil, fmt.Errorf("expected ncdu JSON export array from `ncdu -o-`")
	}

	var root *Node
	switch value := raw.(type) {
	case []any:
		if isSequenceNode(value) {
			root = normalizeNode(value, "root", "", inputType)
		} else {
			children := make([]*Node, 0, len(value))
			for i, item := range value {
				children = append(children, normalizeNode(item, fmt.Sprintf("item-%d", i), "", inputType))
			}
			root = &Node{Name: "root", Path: "root", Type: "dir", Children: children}
			for _, child := range children {
				root.Size += child.Size
			}
		}
	case map[string]any:
		root = normalizeNode(unwrapPossibleRoot(value), "root", "", inputType)
	default:
		return nil, fmt.Errorf("expected a JSON object or array")
	}
	addTotals(root)
	return root, nil
}

func isExportArray(raw any) bool {
	value, ok := raw.([]any)
	if !ok || len(value) < 4 {
		return false
	}
	_, majorOK := value[0].(json.Number)
	_, minorOK := value[1].(json.Number)
	_, metaOK := value[2].(map[string]any)
	switch value[3].(type) {
	case map[string]any, []any:
		return majorOK && minorOK && metaOK
	default:
		return false
	}
}

func validateNcduExport(raw []any) error {
	major, ok := raw[0].(json.Number)
	if !ok || major.String() != "1" {
		return fmt.Errorf("unsupported ncdu export major version: %s", scalarString(raw[0]))
	}
	if _, ok := raw[1].(json.Number); !ok {
		return fmt.Errorf("invalid ncdu export minor version")
	}
	tree, ok := raw[3].([]any)
	if !ok || !isSequenceNode(tree) {
		return fmt.Errorf("invalid ncdu export directory tree")
	}
	return nil
}

func unwrapPossibleRoot(raw map[string]any) any {
	if looksLikeNode(raw) {
		return raw
	}
	for _, key := range []string{"root", "Root", "data", "Data", "scan", "Scan", "tree", "Tree"} {
		if value, ok := raw[key]; ok {
			switch value.(type) {
			case map[string]any, []any:
				return value
			}
		}
	}
	var only map[string]any
	count := 0
	for _, value := range raw {
		if node, ok := value.(map[string]any); ok {
			only = node
			count++
		}
	}
	if count == 1 {
		return only
	}
	return raw
}

func looksLikeNode(raw map[string]any) bool {
	for _, keys := range [][]string{nameKeys, sizeKeys, childKeys} {
		for _, key := range keys {
			if _, ok := raw[key]; ok {
				return true
			}
		}
	}
	return false
}

func isSequenceNode(raw any) bool {
	value, ok := raw.([]any)
	if !ok || len(value) == 0 {
		return false
	}
	info, ok := value[0].(map[string]any)
	return ok && looksLikeNode(info)
}

func normalizeNode(raw any, fallbackName, parentPath, inputType string) *Node {
	switch value := raw.(type) {
	case map[string]any:
		return normalizeMappingNode(value, fallbackName, parentPath, inputType)
	case []any:
		if isSequenceNode(value) {
			return normalizeSequenceNode(value, fallbackName, parentPath, inputType)
		}
		children := make([]*Node, 0, len(value))
		for _, item := range value {
			children = append(children, normalizeNode(item, fallbackName, parentPath, inputType))
		}
		node := &Node{
			Name:     fallbackName,
			Path:     makePath(parentPath, fallbackName),
			Type:     "dir",
			Children: children,
		}
		for _, child := range children {
			node.Size += child.Size
		}
		return node
	default:
		return &Node{
			Name:     fallbackName,
			Path:     makePath(parentPath, fallbackName),
			Size:     numberish(raw),
			Type:     "file",
			Ext:      extensionFor(fallbackName),
			Children: []*Node{},
		}
	}
}

func normalizeSequenceNode(raw []any, fallbackName, parentPath, inputType string) *Node {
	info := raw[0].(map[string]any)
	nameValue := firstString(info, nameKeys)
	pathValue := firstString(info, pathKeys)
	name := nameValue
	if pathValue != "" && (nameValue == "" || nameValue == pathValue) {
		name = displayNameFromPath(pathValue)
	}
	if name == "" {
		name = fallbackName
	}
	nodePath := pathValue
	if nodePath == "" {
		nodePath = makePath(parentPath, name)
	}
	children := make([]*Node, 0, len(raw)-1)
	for i, child := range raw[1:] {
		children = append(children, normalizeNode(child, fmt.Sprintf("item-%d", i), nodePath, inputType))
	}
	size := firstNumber(info, sizeKeys)
	var childSize int64
	for _, child := range children {
		childSize += child.Size
	}
	if inputType == "ncdu" && len(children) > 0 {
		size += childSize
	} else if size <= 0 {
		size = childSize
	}
	nodeType := "file"
	if len(children) > 0 || firstBool(info, dirKeys) {
		nodeType = "dir"
	}
	node := &Node{Name: name, Path: nodePath, Size: size, Type: nodeType, Children: sortedChildren(children)}
	if nodeType == "file" {
		node.Ext = extensionFor(name)
	}
	if value := firstScalar(info, mtimeKeys); value != "" {
		node.MTime = value
	}
	if value := firstString(info, flagKeys); value != "" {
		node.Flag = value
	}
	if value := firstString(info, mimeKeys); value != "" && nodeType != "dir" {
		node.MIME = strings.Split(value, ";")[0]
	} else if mt := mime.TypeByExtension(node.Ext); mt != "" && nodeType != "dir" {
		node.MIME = strings.Split(mt, ";")[0]
	}
	return node
}

func normalizeMappingNode(raw map[string]any, fallbackName, parentPath, inputType string) *Node {
	childrenRaw, hasChildren := extractChildren(raw)
	pathValue := firstString(raw, pathKeys)
	nameValue := firstString(raw, nameKeys)
	name := nameValue
	if pathValue != "" && (nameValue == "" || nameValue == pathValue) {
		name = displayNameFromPath(pathValue)
	}
	if name == "" {
		name = fallbackName
	}
	nodePath := pathValue
	if nodePath == "" {
		nodePath = makePath(parentPath, name)
	}
	if parentPath != "" && nodePath == name {
		nodePath = makePath(parentPath, name)
	}

	var children []*Node
	if hasChildren {
		switch value := childrenRaw.(type) {
		case map[string]any:
			keys := make([]string, 0, len(value))
			for key := range value {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				children = append(children, normalizeNode(value[key], key, nodePath, inputType))
			}
		case []any:
			for i, child := range value {
				children = append(children, normalizeNode(child, fmt.Sprintf("item-%d", i), nodePath, inputType))
			}
		}
	} else {
		children = extractMappingChildren(raw, nodePath, inputType)
	}

	size := firstNumber(raw, sizeKeys)
	if size <= 0 && len(children) > 0 {
		for _, child := range children {
			size += child.Size
		}
	}
	isDir := len(children) > 0 || firstBool(raw, dirKeys)
	nodeType := "file"
	ext := extensionFor(name)
	if isDir {
		nodeType = "dir"
		ext = ""
	}
	node := &Node{Name: name, Path: nodePath, Size: size, Type: nodeType, Ext: ext, Children: sortedChildren(children)}
	if value := firstScalar(raw, mtimeKeys); value != "" {
		node.MTime = value
	}
	if value := firstString(raw, flagKeys); value != "" {
		node.Flag = value
	}
	if value := firstString(raw, mimeKeys); value != "" && !isDir {
		node.MIME = strings.Split(value, ";")[0]
	} else if mt := mime.TypeByExtension(ext); mt != "" && !isDir {
		node.MIME = strings.Split(mt, ";")[0]
	}
	return node
}

func extractChildren(raw map[string]any) (any, bool) {
	for _, key := range childKeys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		switch value.(type) {
		case []any, map[string]any:
			return value, true
		}
	}
	for key, value := range raw {
		if contains(nameKeys, key) || contains(sizeKeys, key) || contains(pathKeys, key) || contains(dirKeys, key) || contains(mtimeKeys, key) || contains(flagKeys, key) || contains(mimeKeys, key) {
			continue
		}
		items, ok := value.([]any)
		if !ok || len(items) == 0 {
			continue
		}
		allMaps := true
		for _, item := range items {
			if _, ok := item.(map[string]any); !ok {
				allMaps = false
				break
			}
		}
		if allMaps {
			return value, true
		}
	}
	return nil, false
}

func extractMappingChildren(raw map[string]any, parentPath, inputType string) []*Node {
	if looksLikeNode(raw) {
		return nil
	}
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var children []*Node
	for _, key := range keys {
		switch raw[key].(type) {
		case map[string]any, []any, json.Number, float64, int, int64:
			children = append(children, normalizeNode(raw[key], key, parentPath, inputType))
		}
	}
	return children
}

func addTotals(root *Node) {
	nextID := 0
	var visit func(*Node, int) (int, int)
	visit = func(node *Node, depth int) (int, int) {
		node.ID = nextID
		nextID++
		node.Depth = depth
		totalCount := 1
		fileCount := 0
		if node.Type != "dir" {
			fileCount = 1
		}
		for _, child := range node.Children {
			childCount, childFiles := visit(child, depth+1)
			totalCount += childCount
			fileCount += childFiles
		}
		node.Items = totalCount - 1
		node.Files = fileCount
		return totalCount, fileCount
	}
	visit(root, 0)
}

func firstString(raw map[string]any, keys []string) string {
	for _, key := range keys {
		if value, ok := raw[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func firstScalar(raw map[string]any, keys []string) string {
	for _, key := range keys {
		switch value := raw[key].(type) {
		case string:
			if value != "" {
				return value
			}
		case json.Number:
			return value.String()
		case float64:
			return strconv.FormatFloat(value, 'f', -1, 64)
		case int:
			return strconv.Itoa(value)
		case int64:
			return strconv.FormatInt(value, 10)
		}
	}
	return ""
}

func firstNumber(raw map[string]any, keys []string) int64 {
	for _, key := range keys {
		if number := numberish(raw[key]); number > 0 {
			return number
		}
	}
	return 0
}

func firstBool(raw map[string]any, keys []string) bool {
	for _, key := range keys {
		if value, ok := raw[key].(bool); ok {
			return value
		}
	}
	return false
}

func numberish(value any) int64 {
	switch v := value.(type) {
	case bool, nil:
		return 0
	case json.Number:
		if i, err := v.Int64(); err == nil {
			if i < 0 {
				return 0
			}
			return i
		}
		f, err := strconv.ParseFloat(v.String(), 64)
		if err != nil || f < 0 {
			return 0
		}
		return int64(f)
	case int:
		if v < 0 {
			return 0
		}
		return int64(v)
	case int64:
		if v < 0 {
			return 0
		}
		return v
	case float64:
		if v < 0 {
			return 0
		}
		return int64(v)
	case string:
		cleaned := strings.ReplaceAll(strings.TrimSpace(v), ",", "")
		if numberPattern.MatchString(cleaned) {
			f, _ := strconv.ParseFloat(cleaned, 64)
			return int64(f)
		}
	}
	return 0
}

func displayNameFromPath(value string) string {
	trimmed := strings.TrimRight(value, "/")
	if trimmed == "" {
		if value == "" {
			return "root"
		}
		return value
	}
	base := path.Base(trimmed)
	if base == "." || base == "/" {
		return trimmed
	}
	return base
}

func makePath(parentPath, name string) string {
	if parentPath == "" {
		return name
	}
	if parentPath == "/" {
		return "/" + strings.TrimLeft(name, "/")
	}
	return strings.TrimRight(parentPath, "/") + "/" + strings.TrimLeft(name, "/")
}

func extensionFor(name string) string {
	base := name
	if index := strings.LastIndexByte(base, '/'); index >= 0 {
		base = base[index+1:]
	}
	dot := strings.LastIndexByte(base, '.')
	if dot <= 0 || dot == len(base)-1 {
		return "[no extension]"
	}
	if base[0] == '.' && dot == 0 {
		return "[no extension]"
	}
	return "." + strings.ToLower(base[dot+1:])
}

func sortedChildren(children []*Node) []*Node {
	sort.SliceStable(children, func(i, j int) bool {
		return children[i].Size > children[j].Size
	})
	return children
}

func scalarString(value any) string {
	switch v := value.(type) {
	case json.Number:
		return v.String()
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
