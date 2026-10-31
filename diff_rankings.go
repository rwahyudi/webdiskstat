package main

import "sort"

const maxDiffRankingRows = 100

type DiffRankings struct {
	GrowingFiles         []DiffRankingRow `json:"growingFiles"`
	ShrinkingFiles       []DiffRankingRow `json:"shrinkingFiles"`
	GrowingDirectories   []DiffRankingRow `json:"growingDirectories"`
	ShrinkingDirectories []DiffRankingRow `json:"shrinkingDirectories"`
}

type DiffRankingRow struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"`
	Before      int64  `json:"before"`
	After       int64  `json:"after"`
	Delta       int64  `json:"delta"`
	BeforeFiles int    `json:"beforeFiles"`
	AfterFiles  int    `json:"afterFiles"`
	Status      string `json:"status"`
}

type diffRankingsBuilder struct {
	growingFiles, shrinkingFiles             []DiffRankingRow
	growingDirectories, shrinkingDirectories []DiffRankingRow
}

func newDiffRankingsBuilder() *diffRankingsBuilder {
	return &diffRankingsBuilder{
		growingFiles:         make([]DiffRankingRow, 0, maxDiffRankingRows),
		shrinkingFiles:       make([]DiffRankingRow, 0, maxDiffRankingRows),
		growingDirectories:   make([]DiffRankingRow, 0, maxDiffRankingRows),
		shrinkingDirectories: make([]DiffRankingRow, 0, maxDiffRankingRows),
	}
}

func (b *diffRankingsBuilder) add(row DiffRankingRow) {
	if row.Delta == 0 {
		return
	}
	var target *[]DiffRankingRow
	switch {
	case row.Type == "dir" && row.Delta > 0:
		target = &b.growingDirectories
	case row.Type == "dir" && row.Delta < 0:
		target = &b.shrinkingDirectories
	case row.Delta > 0:
		target = &b.growingFiles
	default:
		target = &b.shrinkingFiles
	}

	rows := *target
	if len(rows) < maxDiffRankingRows {
		rows = append(rows, row)
	} else {
		worst := 0
		for i := 1; i < len(rows); i++ {
			if rankingBetter(rows[worst], rows[i]) {
				worst = i
			}
		}
		if rankingBetter(row, rows[worst]) {
			rows[worst] = row
		}
	}
	*target = rows
}

// rankingBetter orders greater absolute changes first, then paths ascending.
func rankingBetter(a, b DiffRankingRow) bool {
	if absDiffDelta(a.Delta) != absDiffDelta(b.Delta) {
		return absDiffDelta(a.Delta) > absDiffDelta(b.Delta)
	}
	return a.Path < b.Path
}

func absDiffDelta(delta int64) uint64 {
	if delta < 0 {
		return uint64(-(delta + 1)) + 1
	}
	return uint64(delta)
}

func (b *diffRankingsBuilder) report() DiffRankings {
	finalize := func(rows []DiffRankingRow) []DiffRankingRow {
		sort.Slice(rows, func(i, j int) bool { return rankingBetter(rows[i], rows[j]) })
		return rows
	}
	return DiffRankings{
		GrowingFiles:         finalize(b.growingFiles),
		ShrinkingFiles:       finalize(b.shrinkingFiles),
		GrowingDirectories:   finalize(b.growingDirectories),
		ShrinkingDirectories: finalize(b.shrinkingDirectories),
	}
}
