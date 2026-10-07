package main

import (
	"fmt"
	"html/template"
	"math"
	"strings"
)

// The report is built from format-neutral parts, which render as HTML or as
// Markdown: rich text, table cells and figures.

// text is a paragraph: a format string with arguments, where strings are
// escaped for the output format and anchors and sparklines render natively.
type text struct {
	format string
	args   []any
}

func h(format string, args ...any) text { return text{format, args} }

func (t text) HTML() template.HTML {
	args := make([]any, len(t.args))
	for i, a := range t.args {
		switch a := a.(type) {
		case string:
			args[i] = esc(a)
		case anchor, spark, text:
			args[i] = string(htmlCell(a))
		default:
			args[i] = a
		}
	}
	return template.HTML(fmt.Sprintf(t.format, args...))
}

func (t text) Markdown() string {
	args := make([]any, len(t.args))
	for i, a := range t.args {
		switch a.(type) {
		case string, anchor, spark, text:
			args[i] = mdCell(a)
		default:
			args[i] = a
		}
	}
	return fmt.Sprintf(t.format, args...)
}

// anchor is a link.
type anchor struct{ Href, Text string }

func link(href, text string) anchor { return anchor{href, text} }

// spark is a small trend line.
type spark struct {
	Values []float64
	Tip    string
}

func (s spark) HTML() template.HTML { return sparkline(s.Values, s.Tip) }

// ibar is a number with a small inline bar, share is relative to the column
// maximum.
type ibar struct {
	Share float64
	Text  string
}

func htmlCell(v any) template.HTML {
	switch v := v.(type) {
	case anchor:
		if v.Href == "" {
			return template.HTML(esc(v.Text))
		}
		return template.HTML(fmt.Sprintf(`<a href="%s">%s</a>`, esc(v.Href), esc(v.Text)))
	case spark:
		return v.HTML()
	case ibar:
		return template.HTML(fmt.Sprintf(`<span class="ibar"><i class="bg1" style="width:%.1f%%"></i></span>%s`, v.Share*100, esc(v.Text)))
	case text:
		return v.HTML()
	}
	return template.HTML(esc(fmt.Sprint(v)))
}

func mdCell(v any) string {
	switch v := v.(type) {
	case anchor:
		if v.Href == "" {
			return mdEsc(v.Text)
		}
		return fmt.Sprintf("[%s](%s)", mdEsc(v.Text), v.Href)
	case spark:
		return blockSpark(v.Values)
	case ibar:
		return mdEsc(v.Text)
	case text:
		return v.Markdown()
	}
	return mdEsc(fmt.Sprint(v))
}

var mdEscaper = strings.NewReplacer(
	`\`, `\\`, "`", "\\`", `*`, `\*`, `_`, `\_`, `[`, `\[`, `]`, `\]`,
	`<`, `\<`, `>`, `\>`, `|`, `\|`, "\n", " ",
)

// mdEsc escapes s for use in Markdown text and table cells.
func mdEsc(s string) string { return mdEscaper.Replace(s) }

// blockSpark draws values with block characters, ▁▂▃▄▅▆▇█.
func blockSpark(vals []float64) string {
	const ticks = "▁▂▃▄▅▆▇█"
	var maxv float64
	for _, v := range vals {
		if !math.IsNaN(v) {
			maxv = max(maxv, v)
		}
	}
	runes := []rune(ticks)
	var b strings.Builder
	for _, v := range vals {
		switch {
		case math.IsNaN(v):
			b.WriteRune(' ')
		case maxv == 0:
			b.WriteRune(runes[0])
		default:
			b.WriteRune(runes[int(math.Round(v/maxv*float64(len(runes)-1)))])
		}
	}
	return b.String()
}

// textBar draws a horizontal bar of up to width characters, in eighths.
func textBar(share float64, width int) string {
	const eighths = " ▏▎▍▌▋▊▉"
	n := int(math.Round(share * float64(width) * 8))
	s := strings.Repeat("█", n/8)
	if r := n % 8; r > 0 {
		s += string([]rune(eighths)[r])
	}
	return s
}

// mdTable renders a Markdown table; numeric columns are right aligned.
func mdTable(head []string, rows [][]any, numeric []bool) string {
	var b strings.Builder
	b.WriteString("|")
	for _, h := range head {
		b.WriteString(" " + mdEsc(h) + " |")
	}
	b.WriteString("\n|")
	for c := range head {
		if c < len(numeric) && numeric[c] {
			b.WriteString(" ---: |")
		} else {
			b.WriteString(" --- |")
		}
	}
	b.WriteString("\n")
	for _, r := range rows {
		b.WriteString("|")
		for _, v := range r {
			b.WriteString(" " + mdCell(v) + " |")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// figure is a chart or table.
type figure interface {
	HTML() template.HTML
	Markdown() string
}

// columnFig is a column chart, e.g. counts per year.
type columnFig struct {
	label, x, y string // chart label and column names for the table
	bars        []bar
}

func (f columnFig) data() ([]string, [][]any) {
	var rows [][]any
	for _, b := range f.bars {
		x := b.X
		if b.Muted {
			x += " (incomplete)"
		}
		rows = append(rows, []any{x, fmtInt(int(b.Value))})
	}
	return []string{f.x, f.y}, rows
}

func (f columnFig) HTML() template.HTML {
	return columnChart(f.label, f.bars) + dataDetails(f.data())
}

func (f columnFig) Markdown() string {
	var maxv float64
	for _, b := range f.bars {
		maxv = max(maxv, b.Value)
	}
	head, rows := f.data()
	for i, b := range f.bars {
		rows[i] = append(rows[i], textBar(b.Value/max(maxv, 1), 30))
	}
	return mdTable(append(head, ""), rows, []bool{false, true, false})
}

// dataDetails is the table view behind a chart, folded away.
func dataDetails(head []string, rows [][]any) template.HTML {
	return `<details class="data"><summary>Show data</summary>` + dataTable(head, rows) + `</details>`
}

// lineFig is a line chart with one or more series over shared x values.
type lineFig struct {
	label, x string
	xs       []string
	ss       []series
	pct      bool
	format   func(float64) string
}

func (f lineFig) HTML() template.HTML {
	var names []string
	for _, s := range f.ss {
		names = append(names, s.Name)
	}
	var lg template.HTML
	if len(f.ss) > 1 {
		lg = legend(names...)
	}
	return lg + lineChart(f.label, f.xs, f.ss, f.pct, 5, f.format) + dataDetails(f.data())
}

func (f lineFig) Markdown() string {
	var b strings.Builder
	for _, s := range f.ss {
		fmt.Fprintf(&b, "%s %s–%s: `%s`  \n", mdEsc(s.Name), f.xs[0], f.xs[len(f.xs)-1], blockSpark(s.Values))
	}
	b.WriteString("\n")
	head, rows := f.data()
	b.WriteString(mdTable(head, rows, numericColumns(head, rows)))
	return b.String()
}

func (f lineFig) data() ([]string, [][]any) {
	head := []string{f.x}
	for _, s := range f.ss {
		head = append(head, s.Name)
	}
	var rows [][]any
	for i, x := range f.xs {
		row := []any{x}
		for _, s := range f.ss {
			if v := s.Values[i]; math.IsNaN(v) {
				row = append(row, "–")
			} else {
				row = append(row, f.format(v))
			}
		}
		rows = append(rows, row)
	}
	return head, rows
}

// stackFig is a 100% stacked column chart.
type stackFig struct {
	label, x, unit string
	xs             []string
	ss             []series
}

func (f stackFig) HTML() template.HTML {
	var names []string
	for _, s := range f.ss {
		names = append(names, s.Name)
	}
	return legend(names...) + stackChart(f.label, f.xs, f.ss, 5, f.unit) + dataDetails(f.data())
}

func (f stackFig) Markdown() string {
	head, rows := f.data()
	return mdTable(head, rows, numericColumns(head, rows))
}

func (f stackFig) data() ([]string, [][]any) {
	head := []string{f.x, "Total"}
	for _, s := range f.ss {
		head = append(head, s.Name)
	}
	var rows [][]any
	for i, x := range f.xs {
		var total float64
		for _, s := range f.ss {
			total += s.Values[i]
		}
		row := []any{x, fmtInt(int(total))}
		for _, s := range f.ss {
			row = append(row, fmtPct(s.Values[i]/total))
		}
		rows = append(rows, row)
	}
	return head, rows
}

// barFig is a horizontal bar chart.
type barFig struct {
	label, value string // column names for the table
	items        []hbar
}

func (f barFig) HTML() template.HTML { return hbars(f.items) }

func (f barFig) Markdown() string {
	var maxv float64
	for _, it := range f.items {
		maxv = max(maxv, it.Value)
	}
	var rows [][]any
	for _, it := range f.items {
		label := any(it.Label)
		if it.Href != "" {
			label = anchor{it.Href, it.Label}
		}
		if it.Note != "" {
			label = h("%s (%s)", label, it.Note)
		}
		rows = append(rows, []any{label, it.Text, textBar(it.Value/max(maxv, 1e-9), 24)})
	}
	return mdTable([]string{f.label, f.value, ""}, rows, []bool{false, true, false})
}

// rangeFig is a timeline of ranges.
type rangeFig struct {
	items         []span
	from, to, now float64
}

func (f rangeFig) HTML() template.HTML { return gantt(f.items, f.from, f.to, f.now) }

func (f rangeFig) Markdown() string {
	var rows [][]any
	for _, s := range f.items {
		end := fmt.Sprint(int(s.End))
		if s.Open {
			end = "open"
		}
		rows = append(rows, []any{anchor{s.Href, s.Label}, s.Note, fmt.Sprint(int(s.Start)), end, s.Count})
	}
	return mdTable([]string{"Programme", "Name", "From", "To", "Projects"}, rows, []bool{false, false, true, true, true})
}

// tableFig is a plain table.
type tableFig struct {
	head []string
	rows [][]any
}

func (f tableFig) HTML() template.HTML { return dataTable(f.head, f.rows) }

func (f tableFig) Markdown() string { return mdTable(f.head, f.rows, numericColumns(f.head, f.rows)) }
