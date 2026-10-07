package main

import (
	"fmt"
	"html"
	"html/template"
	"math"
	"regexp"
	"slices"
	"strings"
)

// Charts are rendered here as inline SVG or plain HTML. Marks and hover bands
// carry a data-tip attribute, which a small script on the page shows as a
// tooltip. Colors are CSS classes (s1 fill, l1 stroke, bg1 background, for
// palette slot 1), so light and dark mode need no script.

func esc(s string) string { return html.EscapeString(s) }

const svgW = 760

type bar struct {
	X     string
	Tick  bool // show X as a tick label below the column
	Value float64
	Tip   string
	Muted bool // e.g. an incomplete year
}

type series struct {
	Name   string
	Values []float64 // math.NaN() marks a missing value
}

// frame is the plot area of an SVG chart with a single y axis.
type frame struct {
	w, h, ml, mr, mt, mb float64
	ticks                []float64
	pct                  bool
	b                    strings.Builder
}

func newFrame(h, ml, mr, maxv float64, pct bool) *frame {
	f := &frame{w: svgW, h: h, ml: ml, mr: mr, mt: 10, mb: 24, pct: pct}
	f.ticks = niceTicks(maxv, 4)
	return f
}

func (f *frame) pw() float64         { return f.w - f.ml - f.mr }
func (f *frame) ph() float64         { return f.h - f.mt - f.mb }
func (f *frame) top() float64        { return f.ticks[len(f.ticks)-1] }
func (f *frame) y(v float64) float64 { return f.mt + f.ph() - v/f.top()*f.ph() }

func (f *frame) open(label string) {
	fmt.Fprintf(&f.b, `<svg class="chart" viewBox="0 0 %g %g" role="img" aria-label="%s">`, f.w, f.h, esc(label))
	for _, t := range f.ticks {
		y := f.y(t)
		if t > 0 {
			fmt.Fprintf(&f.b, `<line class="grid" x1="%g" x2="%g" y1="%.1f" y2="%.1f"/>`, f.ml, f.w-f.mr, y, y)
		}
		fmt.Fprintf(&f.b, `<text class="tick" x="%g" y="%.1f" text-anchor="end" dy="0.32em">%s</text>`, f.ml-6, y, fmtTick(t, f.pct))
	}
}

func (f *frame) xlabel(x float64, s string) {
	fmt.Fprintf(&f.b, `<text class="tick" x="%.1f" y="%g" text-anchor="middle">%s</text>`, x, f.h-6, esc(s))
}

func (f *frame) hit(x, w float64, tip string) {
	fmt.Fprintf(&f.b, `<rect class="hit" x="%.1f" y="%g" width="%.1f" height="%g" data-tip="%s"/>`, x, f.mt, w, f.ph(), esc(tip))
}

func (f *frame) close() template.HTML {
	y := f.y(0)
	fmt.Fprintf(&f.b, `<line class="base" x1="%g" x2="%g" y1="%.1f" y2="%.1f"/></svg>`, f.ml, f.w-f.mr, y, y)
	return template.HTML(f.b.String())
}

// niceTicks returns evenly spaced round ticks from zero to at least maxv.
func niceTicks(maxv float64, n int) []float64 {
	if !(maxv > 0) {
		maxv = 1
	}
	raw := maxv / float64(n)
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	step := 10 * mag
	for _, m := range []float64{1, 2, 2.5, 5} {
		if m*mag >= raw {
			step = m * mag
			break
		}
	}
	var ticks []float64
	for i := 0; ; i++ {
		v := float64(i) * step
		ticks = append(ticks, v)
		if v >= maxv*(1-1e-9) {
			return ticks
		}
	}
}

func fmtTick(v float64, pct bool) string {
	if pct {
		return fmt.Sprintf("%g%%", math.Round(v*1000)/10)
	}
	if v >= 1000 {
		return fmtInt(int(math.Round(v)))
	}
	return fmt.Sprintf("%g", math.Round(v*10)/10)
}

// colPath is a column with 4px rounded corners at the data end and a square
// foot on the baseline.
func colPath(x, y, w, h float64) string {
	r := min(4, h, w/2)
	return fmt.Sprintf("M%.1f %.1fV%.1fQ%.1f %.1f %.1f %.1fH%.1fQ%.1f %.1f %.1f %.1fV%.1fZ",
		x, y+h, y+r, x, y, x+r, y, x+w-r, x+w, y, x+w, y+r, y+h)
}

func columnChart(label string, bars []bar) template.HTML {
	var maxv float64
	for _, b := range bars {
		maxv = max(maxv, b.Value)
	}
	f := newFrame(240, 48, 8, maxv, false)
	f.open(label)
	band := f.pw() / float64(len(bars))
	bw := min(24, band*0.7)
	for i, b := range bars {
		x := f.ml + band*float64(i)
		if h := f.ph() * b.Value / f.top(); h > 0 {
			cls := "s1"
			if b.Muted {
				cls = "muted"
			}
			fmt.Fprintf(&f.b, `<path class="%s" d="%s"/>`, cls, colPath(x+(band-bw)/2, f.mt+f.ph()-h, bw, h))
		}
		if b.Tick {
			f.xlabel(x+band/2, b.X)
		}
	}
	for i, b := range bars {
		f.hit(f.ml+band*float64(i), band, b.Tip)
	}
	return f.close()
}

// lineChart draws one or more series over shared x labels; every x carries
// a hover band whose tooltip lists all series. Values at the right edge are
// labeled directly unless they would collide.
func lineChart(label string, xs []string, ss []series, pct bool, every int, format func(float64) string) template.HTML {
	var maxv float64
	for _, s := range ss {
		for _, v := range s.Values {
			if !math.IsNaN(v) {
				maxv = max(maxv, v)
			}
		}
	}
	f := newFrame(240, 48, 52, maxv, pct)
	if pct && f.top() > 1 {
		f.ticks = []float64{0, .25, .5, .75, 1}
	}
	f.open(label)
	step := f.pw() / float64(max(len(xs)-1, 1))
	xp := func(i int) float64 { return f.ml + step*float64(i) }
	for i, x := range xs {
		if every > 0 && i%every == 0 {
			f.xlabel(xp(i), x)
		}
	}
	type end struct {
		y    float64
		text string
	}
	var ends []end
	for si, s := range ss {
		var d strings.Builder
		last := -1
		for i, v := range s.Values {
			if math.IsNaN(v) {
				continue
			}
			cmd := "L"
			if last != i-1 || last < 0 {
				cmd = "M"
			}
			fmt.Fprintf(&d, "%s%.1f %.1f", cmd, xp(i), f.y(v))
			last = i
		}
		fmt.Fprintf(&f.b, `<path class="line l%d" d="%s"/>`, si+1, d.String())
		if last >= 0 {
			v := s.Values[last]
			fmt.Fprintf(&f.b, `<circle class="dot s%d" cx="%.1f" cy="%.1f" r="4"/>`, si+1, xp(last), f.y(v))
			ends = append(ends, end{f.y(v), format(v)})
		}
	}
	ys := make([]float64, len(ends))
	for i, e := range ends {
		ys[i] = e.y
	}
	slices.Sort(ys)
	apart := true
	for i := 1; i < len(ys); i++ {
		apart = apart && ys[i]-ys[i-1] >= 14
	}
	if apart {
		for _, e := range ends {
			fmt.Fprintf(&f.b, `<text class="endlabel" x="%.1f" y="%.1f" dy="0.32em">%s</text>`, f.w-f.mr+8, e.y, esc(e.text))
		}
	}
	for i, x := range xs {
		tip := x
		for _, s := range ss {
			if v := s.Values[i]; !math.IsNaN(v) {
				if len(ss) == 1 {
					tip += ": " + format(v)
				} else {
					tip += "\n" + s.Name + ": " + format(v)
				}
			}
		}
		f.hit(xp(i)-step/2, step, tip)
	}
	return f.close()
}

// stackChart draws 100% stacked columns, one segment per series, separated
// by a 2px surface gap.
func stackChart(label string, xs []string, ss []series, every int, unit string) template.HTML {
	f := newFrame(260, 48, 8, 1, true)
	f.ticks = []float64{0, .25, .5, .75, 1}
	f.open(label)
	band := f.pw() / float64(len(xs))
	bw := min(24, band*0.7)
	for i, x := range xs {
		var total float64
		for _, s := range ss {
			total += s.Values[i]
		}
		left := f.ml + band*float64(i)
		if every > 0 && i%every == 0 {
			f.xlabel(left+band/2, x)
		}
		if total == 0 {
			continue
		}
		tip := fmt.Sprintf("%s · %s %s", x, fmtInt(int(total)), unit)
		y := f.mt + f.ph()
		var segs [][3]float64 // slot, top, height
		for si, s := range ss {
			v := s.Values[i]
			if v <= 0 {
				continue
			}
			h := v / total * f.ph()
			y -= h
			segs = append(segs, [3]float64{float64(si + 1), y, h})
			tip += fmt.Sprintf("\n%s: %s (%s)", s.Name, fmtPct(v/total), fmtInt(int(v)))
		}
		for k, sg := range segs {
			top, h := sg[1], sg[2]
			if k < len(segs)-1 { // surface gap above every segment but the last
				top, h = top+2, h-2
			}
			if h <= 0 {
				continue
			}
			d := fmt.Sprintf("M%.1f %.1fh%.1fv%.1fh%.1fZ", left+(band-bw)/2, top, bw, h, -bw)
			if k == len(segs)-1 {
				d = colPath(left+(band-bw)/2, top, bw, h)
			}
			fmt.Fprintf(&f.b, `<path class="s%d" d="%s"/>`, int(sg[0]), d)
		}
		f.hit(left, band, tip)
	}
	return f.close()
}

func legend(names ...string) template.HTML {
	var b strings.Builder
	b.WriteString(`<div class="legend">`)
	for i, n := range names {
		fmt.Fprintf(&b, `<span><i class="sw bg%d"></i>%s</span>`, i+1, esc(n))
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}

type hbar struct {
	Label string
	Note  string // secondary text after the label
	Href  string
	Value float64
	Text  string // the value as shown at the end of the bar
	Tip   string
}

// hbars is a horizontal bar chart in HTML, so long labels wrap on narrow
// screens.
func hbars(items []hbar) template.HTML {
	var maxv float64
	for _, it := range items {
		maxv = max(maxv, it.Value)
	}
	var b strings.Builder
	b.WriteString(`<div class="hbars">`)
	for _, it := range items {
		label := esc(it.Label)
		if it.Href != "" {
			label = fmt.Sprintf(`<a href="%s">%s</a>`, esc(it.Href), label)
		}
		if it.Note != "" {
			label += ` <small>` + esc(it.Note) + `</small>`
		}
		tip := it.Tip
		if tip == "" {
			tip = it.Label + ": " + it.Text
		}
		fmt.Fprintf(&b, `<div class="hb" data-tip="%s"><div class="hb-l">%s</div><div class="hb-t"><i class="bg1" style="width:calc((100%% - 4.5em) * %.4f)"></i><span>%s</span></div></div>`,
			esc(tip), label, it.Value/max(maxv, 1e-9), esc(it.Text))
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}

// sparkline is a small single series line with a wash below and an end dot.
func sparkline(vals []float64, tip string) template.HTML {
	const w, h, pad = 120.0, 30.0, 4.0
	var maxv float64
	for _, v := range vals {
		maxv = max(maxv, v)
	}
	if maxv == 0 || len(vals) < 2 {
		return ""
	}
	x := func(i int) float64 { return pad + (w-2*pad)*float64(i)/float64(len(vals)-1) }
	y := func(v float64) float64 { return h - pad - (h-2*pad)*v/maxv }
	var line strings.Builder
	for i, v := range vals {
		fmt.Fprintf(&line, "%s%.1f %.1f", map[bool]string{true: "M", false: "L"}[i == 0], x(i), y(v))
	}
	n := len(vals) - 1
	return template.HTML(fmt.Sprintf(`<svg class="spark" viewBox="0 0 %g %g" width="%g" height="%g" role="img" aria-label="%s" data-tip="%s">`+
		`<path class="wash" d="%sL%.1f %gL%.1f %gZ"/><path class="line l1" d="%s"/><circle class="dot s1" cx="%.1f" cy="%.1f" r="3"/></svg>`,
		w, h, w, h, esc(tip), esc(tip), line.String(), x(n), h-pad, x(0), h-pad, line.String(), x(n), y(vals[n])))
}

type span struct {
	Label, Note, Tip, Href string
	Start, End             float64 // fractional years
	Count                  int     // projects behind the span
	Open                   bool    // no end date known, End is the snapshot
}

// gantt draws spans on a shared year axis, with a marker for the snapshot.
func gantt(spans []span, from, to, now float64) template.HTML {
	pos := func(v float64) float64 { return (v - from) / (to - from) * 100 }
	var b strings.Builder
	b.WriteString(`<div class="gantt"><div class="g-row g-axis"><div class="g-l"></div><div class="g-t">`)
	for y := math.Ceil(from/5) * 5; y <= to; y += 5 {
		fmt.Fprintf(&b, `<span style="left:%.2f%%">%g</span>`, pos(y), y)
	}
	b.WriteString(`</div></div>`)
	for _, s := range spans {
		label := esc(s.Label)
		if s.Href != "" {
			label = fmt.Sprintf(`<a href="%s">%s</a>`, esc(s.Href), label)
		}
		if s.Note != "" {
			label += ` <small>` + esc(s.Note) + `</small>`
		}
		fmt.Fprintf(&b, `<div class="g-row" data-tip="%s"><div class="g-l">%s</div><div class="g-t"><b class="now" style="left:%.2f%%"></b><i class="bg1" style="left:%.2f%%;width:%.2f%%"></i></div></div>`,
			esc(s.Tip), label, pos(now), pos(s.Start), max(pos(s.End)-pos(s.Start), 0.6))
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}

var numCell = regexp.MustCompile(`^[-+]?[\d,.]+\s?(%|years|months)?$`)

// numericColumns reports for each column whether it holds only numbers.
func numericColumns(head []string, rows [][]any) []bool {
	numeric := make([]bool, len(head))
	for c := range head {
		numeric[c] = true
		for _, r := range rows {
			switch v := r[c].(type) {
			case int:
			case string:
				if v != "" && v != "–" && !numCell.MatchString(v) {
					numeric[c] = false
				}
			default:
				numeric[c] = false
			}
		}
	}
	return numeric
}

// dataTable renders rows as an HTML table; numeric columns are right aligned
// with tabular figures.
func dataTable(head []string, rows [][]any) template.HTML {
	numeric := numericColumns(head, rows)
	class := func(c int) string {
		if numeric[c] {
			return ` class="num"`
		}
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="tablewrap"><table><thead><tr>`)
	for c, h := range head {
		fmt.Fprintf(&b, `<th%s>%s</th>`, class(c), esc(h))
	}
	b.WriteString(`</tr></thead><tbody>`)
	for _, r := range rows {
		b.WriteString(`<tr>`)
		for c, v := range r {
			fmt.Fprintf(&b, `<td%s>%s</td>`, class(c), htmlCell(v))
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table></div>`)
	return template.HTML(b.String())
}
