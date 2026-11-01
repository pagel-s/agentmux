package main

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

type consoleEditorPosition struct{ row, column int }

// Cursor positions use display cells, so vertical movement also works for wide
// characters and wrapped lines. A preferred column survives shorter rows.
func consoleEditorPositions(text []rune, width int) []consoleEditorPosition {
	width = max(1, width)
	positions := make([]consoleEditorPosition, len(text)+1)
	for i := range positions {
		// Interior rune indices of one displayed character are not cursor
		// destinations for vertical movement (emoji joiners, accents, etc.).
		positions[i].row = -1
	}
	row, column := 0, 0
	index := 0
	graphemes := uniseg.NewGraphemes(string(text))
	for graphemes.Next() {
		cluster := graphemes.Str()
		// CRLF is one grapheme but still ends a logical editor row. Its CR
		// remains visibly escaped, matching consoleEditor rendering.
		newline := strings.HasSuffix(cluster, "\n")
		visible := strings.TrimSuffix(cluster, "\n")
		cells := ansi.StringWidth(consoleContent(visible, utf8.RuneCountInString(visible)))
		if visible != "" && column > 0 && column+cells > width {
			row++
			column = 0
		}
		positions[index] = consoleEditorPosition{row, column}
		if newline {
			row++
			column = 0
		} else {
			column += cells
		}
		index += utf8.RuneCountInString(cluster)
	}
	if column >= width {
		row += column / width
		column %= width
	}
	positions[len(text)] = consoleEditorPosition{row, column}
	return positions
}

func consoleEditorMove(text []rune, cursor, width, delta, preferred int, keepColumn bool) (int, int) {
	cursor = max(0, min(cursor, len(text)))
	positions := consoleEditorPositions(text, width)
	currentIndex := cursor
	for currentIndex > 0 && positions[currentIndex].row < 0 {
		currentIndex--
	}
	current := positions[currentIndex]
	if !keepColumn {
		preferred = current.column
	}
	wanted := current.row + delta
	if wanted < 0 || wanted > positions[len(text)].row {
		return cursor, preferred
	}
	best, distance := cursor, int(^uint(0)>>1)
	for i, position := range positions {
		if position.row != wanted {
			continue
		}
		d := position.column - preferred
		if d < 0 {
			d = -d
		}
		if d < distance {
			best, distance = i, d
		}
	}
	return best, preferred
}

func consoleEditorLineEdge(text []rune, cursor int, end bool) int {
	cursor = max(0, min(cursor, len(text)))
	if end {
		for cursor < len(text) && text[cursor] != '\n' {
			cursor++
		}
	} else {
		for cursor > 0 && text[cursor-1] != '\n' {
			cursor--
		}
	}
	return cursor
}
