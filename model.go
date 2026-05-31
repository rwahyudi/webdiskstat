package main

type Node struct {
	Name     string  `json:"name"`
	Path     string  `json:"path"`
	Size     int64   `json:"size"`
	Type     string  `json:"type"`
	Ext      string  `json:"ext"`
	Children []*Node `json:"children"`
	ID       int     `json:"id"`
	Depth    int     `json:"depth"`
	Items    int     `json:"items"`
	Files    int     `json:"files"`
	MTime    string  `json:"mtime,omitempty"`
	Flag     string  `json:"flag,omitempty"`
	MIME     string  `json:"mime,omitempty"`
}
