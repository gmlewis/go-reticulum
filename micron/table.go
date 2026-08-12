// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package micron

import "strings"

// FormatTable renders a markdown table (given as its raw source lines)
// to micron box-drawing output. It mirrors Python's
// MarkdownToMicron.format_table, using the default "c" (center)
// alignment scope for the whole table. rows[0] is the header, rows[1]
// is the separator (used to derive per-column alignments) and the
// remaining rows are data.
//
// The header row has its backticks escaped via escapeLiterals (matching
// Python); data rows are appended raw so inline micron formatting in
// data cells is preserved. Border lines are also escaped.
func (c *Converter) FormatTable(rows []string) []string {
	return c.formatTableImpl(rows, "c", true)
}

// FormatTableRaw renders a markdown table without applying inline
// formatting to cells, mirroring Python's format_table_raw.
func (c *Converter) FormatTableRaw(rows []string) []string {
	return c.formatTableImpl(rows, "c", false)
}

func (c *Converter) formatTableImpl(rows []string, align string, formatInline bool) []string {
	if len(rows) < 2 {
		return rows
	}
	headerCells := parseTableRow(rows[0])
	alignments := parseTableAlignments(rows[1])
	for len(alignments) < len(headerCells) {
		alignments = append(alignments, "left")
	}
	alignments = alignments[:len(headerCells)]

	var dataRows [][]string
	for _, row := range rows[2:] {
		cells := parseTableRow(row)
		for len(cells) < len(headerCells) {
			cells = append(cells, "")
		}
		cells = cells[:len(headerCells)]
		dataRows = append(dataRows, cells)
	}

	numCols := len(headerCells)
	colWidths := make([]int, numCols)
	allRows := [][]string{headerCells}
	allRows = append(allRows, dataRows...)
	for _, row := range allRows {
		for i, cell := range row {
			formatted := cell
			if formatInline {
				formatted = c.formatInline(cell)
			}
			w := c.visibleWidth(formatted)
			if w > colWidths[i] {
				colWidths[i] = w
			}
		}
	}
	for i := range colWidths {
		if colWidths[i] < TableMinColWidth {
			colWidths[i] = TableMinColWidth
		}
	}

	totalWidth := 0
	for _, w := range colWidths {
		totalWidth += w
	}
	totalWidth += numCols*3 + 1
	if totalWidth > c.MaxWidth {
		excess := totalWidth - c.MaxWidth
		indexed := make([][2]int, numCols)
		for i, w := range colWidths {
			indexed[i] = [2]int{i, w}
		}
		sortDesc(indexed)
		for _, p := range indexed {
			if excess <= 0 {
				break
			}
			reduction := excess
			if p[1]-TableMinColWidth < reduction {
				reduction = p[1] - TableMinColWidth
			}
			colWidths[p[0]] -= reduction
			excess -= reduction
		}
	}

	var result []string
	if align != "" {
		result = append(result, "`"+align)
	}

	// Top border
	border := TableTL
	for i, w := range colWidths {
		border += strings.Repeat(TableH, w+2)
		if i < numCols-1 {
			border += TableTM
		} else {
			border += TableTR
		}
	}
	result = append(result, escapeLiterals(border))

	// Header row
	headerLine := TableV
	for i, cell := range headerCells {
		formatted := cell
		if formatInline {
			formatted = c.formatInline(cell)
		}
		padded := c.padCell(formatted, colWidths[i], "left")
		headerLine += " " + padded + " " + TableV
	}
	result = append(result, escapeLiterals(headerLine))

	// Separator row
	sepLine := TableML
	for i, w := range colWidths {
		sepLine += strings.Repeat(TableH, w+2)
		if i < numCols-1 {
			sepLine += TableMM
		} else {
			sepLine += TableMR
		}
	}
	result = append(result, escapeLiterals(sepLine))

	// Data rows
	for _, row := range dataRows {
		rowLine := TableV
		for i, cell := range row {
			formatted := cell
			if formatInline {
				formatted = c.formatInline(cell)
			}
			padded := c.padCell(formatted, colWidths[i], alignments[i])
			rowLine += " " + padded + " " + TableV
		}
		result = append(result, rowLine)
	}

	// Bottom border
	border = TableBL
	for i, w := range colWidths {
		border += strings.Repeat(TableH, w+2)
		if i < numCols-1 {
			border += TableBM
		} else {
			border += TableBR
		}
	}
	result = append(result, escapeLiterals(border))

	if align != "" {
		result = append(result, "`a")
	}
	return result
}

// parseTableRow splits a markdown table row into cells, mirroring
// Python's _parse_table_row. Leading and trailing pipes are stripped and
// backslash-escaped pipes are preserved.
func parseTableRow(line string) []string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "|") {
		line = line[1:]
	}
	if strings.HasSuffix(line, "|") {
		line = line[:len(line)-1]
	}
	var cells []string
	current := ""
	escaped := false
	for _, char := range line {
		if escaped {
			current += string(char)
			escaped = false
			continue
		}
		switch char {
		case '\\':
			escaped = true
		case '|':
			cells = append(cells, strings.TrimSpace(current))
			current = ""
		default:
			current += string(char)
		}
	}
	cells = append(cells, strings.TrimSpace(current))
	return cells
}

// parseTableAlignments derives per-column alignment from a markdown table
// separator row, mirroring Python's _parse_table_alignments.
func parseTableAlignments(line string) []string {
	cells := parseTableRow(line)
	var alignments []string
	for _, cell := range cells {
		cell = strings.TrimSpace(cell)
		switch {
		case strings.HasPrefix(cell, ":") && strings.HasSuffix(cell, ":"):
			alignments = append(alignments, "center")
		case strings.HasSuffix(cell, ":"):
			alignments = append(alignments, "right")
		default:
			alignments = append(alignments, "left")
		}
	}
	return alignments
}

// sortDesc sorts index/width pairs by width descending (stable enough for
// the proportional column reduction loop).
func sortDesc(s [][2]int) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1][1] < s[j][1]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
