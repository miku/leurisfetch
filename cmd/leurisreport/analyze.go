package main

import (
	"cmp"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type page struct {
	Title    string
	Snapshot string
	Intro    text
	Tiles    []tile
	Sections []section
	Notes    []text
}

type tile struct{ Label, Value, Note string }

type section struct {
	ID, Title string
	Lead      []text
	Tiles     []tile
	Blocks    []block
}

// block is one figure: a chart, table, or set of cards.
type block struct {
	ID              string // set by assignIDs for titled blocks
	Title, Subtitle string
	Half            bool // half width on large screens
	Figs            []figure
	Cards           []card
	Notes           []text
}

type card struct {
	Kicker, Title string
	Body          text
	Spark         *spark
	Note          string
}

func build(d *data) *page {
	pg := &page{
		Title:    "Research at Leipzig University",
		Snapshot: date(d.snapshot),
		Intro: h(`A portrait of research at Leipzig University, drawn from %s publications and %s research projects in %s, the university's research information system. Where the report says "now", it means the date of the snapshot, %s, the most recent update in the data.`,
			fmtInt(len(d.pubs)), fmtInt(len(d.projs)), link("https://leuris.uni-leipzig.de", "LEURIS"), date(d.snapshot)),
		Tiles: overview(d),
		Sections: []section{
			timeline(d),
			researchAreas(d),
			fundingSection(d),
			publishing(d),
			people(d),
			curiosities(d),
		},
		Notes: notes(d),
	}
	assignIDs(pg)
	return pg
}

// assignIDs gives every titled block an ID derived from its title, unique
// across the page, so it can be linked to.
func assignIDs(pg *page) {
	seen := make(map[string]bool)
	for _, s := range pg.Sections {
		seen[s.ID] = true
	}
	for i := range pg.Sections {
		for j := range pg.Sections[i].Blocks {
			b := &pg.Sections[i].Blocks[j]
			if b.Title == "" {
				continue
			}
			id := slug(b.Title)
			for n := 2; id == "" || seen[id]; n++ {
				id = fmt.Sprintf("%s-%d", slug(b.Title), n)
			}
			seen[id] = true
			b.ID = id
		}
	}
}

// pubYears returns the range of publication years.
func pubYears(d *data) (lo, hi int) {
	lo = math.MaxInt
	for _, p := range d.pubs {
		if p.Year > 0 {
			lo, hi = min(lo, p.Year), max(hi, p.Year)
		}
	}
	return lo, hi
}

// plausible reports whether a project has dates that make sense.
func (p *project) plausible() bool {
	return p.start.Year() >= 1900 && !p.end.IsZero() && !p.end.Before(p.start)
}

func (p *project) runningAt(t time.Time) bool {
	return !p.start.IsZero() && !p.start.After(t) && p.end.After(t)
}

// yearAxis returns labels for the years from..to, and an index function.
func yearAxis(from, to int) []string {
	var xs []string
	for y := from; y <= to; y++ {
		xs = append(xs, strconv.Itoa(y))
	}
	return xs
}

var journalPunct = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// journalKey folds case, "&" and punctuation, so "PLoS one" and "PLOS ONE"
// count as one journal.
func journalKey(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, "&", " and "))
	s = strings.TrimSpace(journalPunct.ReplaceAllString(s, " "))
	return strings.TrimPrefix(s, "the ")
}

func overview(d *data) []tile {
	researchers := make(map[string]bool)
	for _, p := range d.pubs {
		for _, a := range p.Authors {
			if a.FisPersid != "" {
				researchers[a.FisPersid] = true
			}
		}
	}
	for _, p := range d.projs {
		for _, a := range p.people() {
			if a.FisPersid != "" {
				researchers[a.FisPersid] = true
			}
		}
	}
	var (
		journals    = make(map[string]bool)
		areas       = make(map[string]bool)
		teams       []float64
		en, known   int
		running     int
		lo, hi      = pubYears(d)
		withFunding int
	)
	for _, p := range d.pubs {
		if p.Journal != "" {
			journals[journalKey(p.Journal)] = true
		}
		areas[p.area] = true
		teams = append(teams, float64(len(p.Authors)))
		if p.Year == d.lastFull && p.lang != "" {
			known++
			if p.lang == "English" {
				en++
			}
		}
	}
	for _, p := range d.projs {
		if p.runningAt(d.snapshot) {
			running++
		}
		if len(p.funders) > 0 {
			withFunding++
		}
	}
	return []tile{
		{"Publications", fmtInt(len(d.pubs)), fmt.Sprintf("dated %d to %d", lo, hi)},
		{"Research projects", fmtInt(len(d.projs)), fmtInt(running) + " running on the snapshot date"},
		{"Researchers", fmtInt(len(researchers)), "university members named as authors or project staff"},
		{"Faculties and institutions", fmtInt(len(areas)), fmtInt(len(d.units)) + " organisational units in all"},
		{"Journals", fmtInt(len(journals)), "distinct journal titles"},
		{"Median team", fmt.Sprintf("%g", median(teams)), "authors per publication"},
		{"In English", pct(en, known), fmt.Sprintf("of %d publications with a known language", d.lastFull)},
		{"Funding bodies", fmtInt(len(funderNames(d))), fmtInt(withFunding) + " projects name a funder"},
	}
}

func funderNames(d *data) counter {
	c := make(counter)
	for _, p := range d.projs {
		for _, f := range p.funders {
			c[f]++
		}
	}
	return c
}

// consortium matches the ids of DFG collaborative programmes in titles:
// Collaborative Research Centres (SFB, TRR, in English CRC), Research Units
// (FOR), Research Training Groups (GRK, RTG), Clinical Research Units (KFO)
// and Research Centres (FZT). Priority Programmes (SPP) are left out, as
// they are nationwide umbrellas rather than local consortia.
var consortium = regexp.MustCompile(`\b(SFB|TRR|FOR|GRK|KFO|FZT|EXC|CRC|RTG)[ -]?(\d{2,4})\b`)

// consortiumName matches a programme's own title, like "SFB 1052: Obesity
// Mechanisms", but not a subproject like "SFB 1052/C04: ...".
var consortiumName = regexp.MustCompile(`^\s*(SFB|TRR|FOR|GRK|KFO|FZT|EXC|CRC|RTG)[ -]?\d{2,4}\b\s*[:–-]?\s*([\pL\pN"„“].{7,})$`)

// subproject matches names that only say a project is part of a programme.
var subproject = regexp.MustCompile(`(?i)^(teilprojekt|subproject|sub-project|project|projekt|tp)\b`)

func consortiumKey(m []string) string {
	kind := m[1]
	switch kind {
	case "CRC":
		kind = "SFB"
	case "RTG":
		kind = "GRK"
	}
	return kind + " " + m[2]
}

func timeline(d *data) section {
	s := section{ID: "timeline", Title: "Timeline"}
	lo, hi := pubYears(d)
	perYear := make(map[int]int)
	for _, p := range d.pubs {
		perYear[p.Year]++
	}
	var (
		bars          []bar
		peakY, peakN  int
		jumpY, jumpN  int
		steadyFrom    = d.lastFull
		firstCreated  time.Time
		createdPerDay = make(counter)
	)
	for y := lo; y <= hi; y++ {
		n := perYear[y]
		b := bar{X: strconv.Itoa(y), Tick: y%5 == 0, Value: float64(n), Tip: fmt.Sprintf("%d: %s publications", y, fmtInt(n)), Muted: y > d.lastFull}
		if b.Muted {
			b.Tip += " so far"
		}
		bars = append(bars, b)
		if y <= d.lastFull && n > peakN {
			peakY, peakN = y, n
		}
		if y > lo && y <= d.lastFull && n-perYear[y-1] > jumpN {
			jumpY, jumpN = y, n-perYear[y-1]
		}
	}
	for y := d.lastFull; y >= lo && perYear[y] >= 1000; y-- {
		steadyFrom = y
	}
	for _, p := range d.pubs {
		if p.created.IsZero() {
			continue
		}
		if firstCreated.IsZero() || p.created.Before(firstCreated) {
			firstCreated = p.created
		}
		createdPerDay[p.created.Format(time.DateOnly)]++
	}
	bigDay := createdPerDay.top(1)[0]
	bigDayT, _ := time.Parse(time.DateOnly, bigDay.Key)
	imported := h(`The oldest record in the system was created on %s, and on %s alone %s publication records were entered.`,
		date(firstCreated), date(bigDayT), fmtInt(bigDay.N))
	if bigDayT.Format(time.DateOnly) == firstCreated.Format(time.DateOnly) {
		imported = h(`The oldest record in the system was created on %s, the day %s publication records were entered: the initial import.`,
			date(firstCreated), fmtInt(bigDay.N))
	}
	s.Lead = append(s.Lead,
		h(`LEURIS lists publications from %d to %d. Output peaks in %d with %s publications, and every year since %d has more than a thousand.`,
			lo, hi, peakY, fmtInt(peakN), steadyFrom),
		h(`Read the early part of the curve with care. %s Everything before is backfilled, and steps such as the jump from %d to %d (+%s) more likely mark units starting to report than a sudden rise in output. %d is incomplete, as the snapshot dates from %s.`,
			imported, jumpY-1, jumpY, fmtInt(jumpN), hi, date(d.snapshot)))
	s.Blocks = append(s.Blocks, block{
		Title:    "Publications per year",
		Subtitle: fmt.Sprintf("By year of publication, %d–%d; %d is not complete yet", lo, hi, hi),
		Figs:     []figure{columnFig{"Publications per year", "Year", "Publications", bars}},
	})

	// Projects started and running per year.
	const from = 2000
	var (
		started, running = make(map[int]int), make(map[int]int)
		earlier, openEnd int
		lags             []float64
	)
	for _, p := range d.projs {
		if p.start.Year() < 1900 {
			continue
		}
		if p.start.Year() < from {
			earlier++
		}
		started[p.start.Year()]++
		if !p.created.IsZero() && p.start.After(firstCreated) && p.start.Before(d.snapshot) {
			lags = append(lags, years(p.start, p.created))
		}
		if p.end.IsZero() {
			openEnd++
			continue
		}
		if !p.plausible() {
			continue
		}
		for y := max(p.start.Year(), from); y <= min(p.end.Year(), d.snapshot.Year()); y++ {
			running[y]++
		}
	}
	var (
		sbars    []bar
		runVals  []float64
		xs       = yearAxis(from, d.snapshot.Year())
		peakS, n int
	)
	for y := from; y <= d.snapshot.Year(); y++ {
		sbars = append(sbars, bar{X: strconv.Itoa(y), Tick: y%5 == 0, Value: float64(started[y]),
			Tip: fmt.Sprintf("%d: %s projects started", y, fmtInt(started[y])), Muted: y > d.lastFull})
		runVals = append(runVals, float64(running[y]))
		if started[y] > n {
			peakS, n = y, started[y]
		}
	}
	lag := median(lags)
	s.Lead = append(s.Lead, h(`Research projects follow a different curve. Starts peak in %d with %s new projects and fall off since. Part of that is lag: projects that began after LEURIS went live were entered a median of %s after they started, so recent years will still fill up.`,
		peakS, fmtInt(n), fmtYears(lag)))
	s.Blocks = append(s.Blocks,
		block{
			Title:    "Projects started per year",
			Subtitle: fmt.Sprintf("By start date; %s projects started before %d", fmtInt(earlier), from),
			Figs:     []figure{columnFig{"Projects started per year", "Year", "Projects started", sbars}},
		},
		block{
			Title:    "Projects running per year",
			Subtitle: fmt.Sprintf("Projects with a plausible start and end date; %s without an end date are left out", fmtInt(openEnd)),
			Figs: []figure{lineFig{"Projects running per year", "Year", xs, []series{{"Running", runVals}}, false,
				func(v float64) string { return fmtInt(int(v)) }}},
		})

	s.Blocks = append(s.Blocks, consortia(d), firsts(d))
	return s
}

// consortia draws the lifetime of large collaborative programmes, as far as
// their (sub)projects are recorded.
func consortia(d *data) block {
	type group struct {
		key        string
		projs      []*project
		start, end time.Time
		open       bool
		names      counter // candidate names, from titles and superordinate projects
		main       *project
	}
	groups := make(map[string]*group)
	for _, p := range d.projs {
		if p.start.Year() < 1900 {
			continue
		}
		keys := make(map[string]bool)
		for _, m := range consortium.FindAllStringSubmatch(p.TitleDe+" | "+p.TitleEn+" | "+p.Superordinate, -1) {
			keys[consortiumKey(m)] = true
		}
		for k := range keys {
			g, ok := groups[k]
			if !ok {
				g = &group{key: k, start: p.start, names: make(counter)}
				groups[k] = g
			}
			g.projs = append(g.projs, p)
			if p.start.Before(g.start) {
				g.start = p.start
			}
			if p.end.IsZero() {
				g.open = true
			} else if p.end.After(g.end) && !p.end.Before(p.start) {
				g.end = p.end
			}
			// A project titled after the programme names it best, the
			// superordinate project of its subprojects second best.
			for _, t := range []string{p.TitleEn, p.TitleDe} {
				if m := consortiumName.FindStringSubmatch(t); m != nil && !subproject.MatchString(m[2]) && consortiumKey(consortium.FindStringSubmatch(t)) == k {
					if g.main == nil {
						g.main = p
					}
					g.names[strings.Trim(m[2], ` "„“”`)] += 1000
					break
				}
			}
			if m := consortiumName.FindStringSubmatch(p.Superordinate); m != nil && !subproject.MatchString(m[2]) && consortiumKey(consortium.FindStringSubmatch(p.Superordinate)) == k {
				g.names[strings.Trim(m[2], ` "„“”`)]++
			}
		}
	}
	var gs []*group
	for _, g := range groups {
		if len(g.projs) >= 2 || g.main != nil {
			gs = append(gs, g)
		}
	}
	slices.SortFunc(gs, func(a, b *group) int {
		return cmp.Or(a.start.Compare(b.start), cmp.Compare(a.key, b.key))
	})
	var (
		spans    []span
		from, to = math.Inf(1), fracYear(d.snapshot)
		now      = fracYear(d.snapshot)
	)
	for _, g := range gs {
		var name string
		if len(g.names) > 0 {
			name = g.names.top(1)[0].Key
		}
		href := g.projs[0].Link
		if g.main != nil {
			href = g.main.Link
		}
		end, until := g.end, ""
		if end.IsZero() || (g.open && end.Before(d.snapshot)) {
			end, until = d.snapshot, "open"
		}
		tip := g.key
		if name != "" {
			tip += ": " + shorten(name, 90)
		}
		tip += fmt.Sprintf("\n%d–%s, %s in LEURIS", g.start.Year(), cmp.Or(until, strconv.Itoa(end.Year())), plural(len(g.projs), "project", "projects"))
		spans = append(spans, span{
			Label: g.key, Note: shorten(name, 52), Tip: tip, Href: href,
			Start: fracYear(g.start), End: fracYear(end), Count: len(g.projs), Open: until != "",
		})
		from, to = min(from, fracYear(g.start)), max(to, fracYear(end))
	}
	from = math.Floor(from/5) * 5
	return block{
		Title: "Collaborative research programmes",
		Subtitle: fmt.Sprintf("%d DFG Collaborative Research Centres (SFB/TRR), Research Units (FOR), Research Training Groups (GRK) and the like, found by their ids in project titles; bars span their recorded (sub)projects, the line marks the snapshot",
			len(gs)),
		Figs: []figure{rangeFig{spans, from, math.Ceil(to), now}},
	}
}

// firstMentions lists topics with the year their first title appears.
var firstMentions = []struct {
	Topic string
	Re    *regexp.Regexp
}{
	{"Open access", regexp.MustCompile(`open access`)},
	{"Big data", regexp.MustCompile(`big data`)},
	{"Social media", regexp.MustCompile(`social media|soziale medien`)},
	{"Twitter", regexp.MustCompile(`twitter`)},
	{"CRISPR", regexp.MustCompile(`crispr`)},
	{"Deep learning", regexp.MustCompile(`deep learning|deep neural`)},
	{"Ebola", regexp.MustCompile(`ebola`)},
	{"Zika", regexp.MustCompile(`\bzika`)},
	{"Brexit", regexp.MustCompile(`brexit`)},
	{"Blockchain", regexp.MustCompile(`blockchain`)},
	{"Microplastics", regexp.MustCompile(`microplastic|mikroplastik`)},
	{"Fridays for Future", regexp.MustCompile(`fridays for future`)},
	{"COVID-19", regexp.MustCompile(`covid|sars-cov-2`)},
	{"Long COVID", regexp.MustCompile(`long[- ]covid|post-covid[- ](19[- ])?(syndrom|condition)|post-acute sequelae`)},
	{"Mpox", regexp.MustCompile(`mpox|monkeypox|affenpocken`)},
	{"ChatGPT & LLMs", regexp.MustCompile(`chatgpt|large language model|\bllms?\b`)},
	{"Quantum computing", regexp.MustCompile(`quantum comput`)},
	{"TikTok", regexp.MustCompile(`tiktok`)},
}

func firsts(d *data) block {
	type first struct {
		topic string
		pub   *publication
		n     int
	}
	var fs []first
	lower := make([]string, len(d.pubs))
	for i, p := range d.pubs {
		lower[i] = strings.ToLower(p.Title)
	}
	for _, m := range firstMentions {
		f := first{topic: m.Topic}
		for i, p := range d.pubs {
			if p.Year == 0 || !m.Re.MatchString(lower[i]) {
				continue
			}
			f.n++
			if f.pub == nil || p.Year < f.pub.Year || (p.Year == f.pub.Year && p.ID < f.pub.ID) {
				f.pub = p
			}
		}
		if f.pub != nil {
			fs = append(fs, f)
		}
	}
	slices.SortStableFunc(fs, func(a, b first) int { return cmp.Compare(a.pub.Year, b.pub.Year) })
	var rows [][]any
	for _, f := range fs {
		rows = append(rows, []any{strconv.Itoa(f.pub.Year), f.topic, link(f.pub.Link, shorten(f.pub.Title, 110)), fmtInt(f.n)})
	}
	return block{
		Title:    "First appearances",
		Subtitle: "The earliest title in LEURIS mentioning a topic, and how many titles mention it in all",
		Figs:     []figure{tableFig{[]string{"Year", "Topic", "First title", "Titles"}, rows}},
	}
}

// topics are tracked by keywords in titles, as a share of all titles.
var topics = []struct {
	Name string
	Re   *regexp.Regexp
}{
	{"COVID-19 & pandemics", regexp.MustCompile(`covid|sars-cov|coronavirus|\bcorona\b|corona-|pandemi`)},
	{"AI & machine learning", regexp.MustCompile(`machine learning|deep learning|artificial intelligence|künstliche intelligenz|maschinelles lernen|neural network|neuronale netz|chatgpt|large language model|\bai\b|\bki\b`)},
	{"Climate", regexp.MustCompile(`climat|klima`)},
	{"Biodiversity", regexp.MustCompile(`biodiversit`)},
	{"Sustainability", regexp.MustCompile(`sustainab|nachhaltig`)},
	{"Digital", regexp.MustCompile(`digital`)},
	{"Migration & refugees", regexp.MustCompile(`\bmigrant|refugee|flüchtling|\basyl|immigra|zuwander|einwander`)},
	{"Democracy & populism", regexp.MustCompile(`democra|demokrat|populis|authoritarian|autoritär|right-wing|rechtsextrem`)},
	{"Obesity", regexp.MustCompile(`obes|adipos`)},
	{"Gender", regexp.MustCompile(`gender|geschlecht`)},
	{"China", regexp.MustCompile(`china|chinese|chines`)},
	{"Ukraine & Russia", regexp.MustCompile(`ukrain|russia|russland|russisch`)},
}

func researchAreas(d *data) section {
	s := section{ID: "areas", Title: "Research areas"}
	type area struct {
		id          string
		pubs, projs int
		perYear     map[int]int
		en, known   int
		teams       []float64
		terms       counter
	}
	var (
		areas = make(map[string]*area)
		all   = make(counter)
	)
	get := func(id string) *area {
		a, ok := areas[id]
		if !ok {
			a = &area{id: id, perYear: make(map[int]int), terms: make(counter)}
			areas[id] = a
		}
		return a
	}
	for _, p := range d.pubs {
		a := get(p.area)
		a.pubs++
		a.perYear[p.Year]++
		a.teams = append(a.teams, float64(len(p.Authors)))
		if p.lang != "" {
			a.known++
			if p.lang == "English" {
				a.en++
			}
		}
		for _, t := range p.terms {
			a.terms[t]++
			all[t]++
		}
	}
	for _, p := range d.projs {
		get(p.area).projs++
	}
	var list []*area
	for _, a := range areas {
		list = append(list, a)
	}
	slices.SortFunc(list, func(a, b *area) int { return cmp.Or(cmp.Compare(b.pubs, a.pubs), cmp.Compare(a.id, b.id)) })

	const shown = 16
	var (
		rows   [][]any
		nall   = all.total()
		maxPub = float64(list[0].pubs)
		other  area
	)
	for i, a := range list {
		if i >= shown || a.id == "" {
			other.pubs += a.pubs
			other.projs += a.projs
			continue
		}
		rest := make(counter, len(a.terms))
		for w := range a.terms {
			rest[w] = all[w] - a.terms[w]
		}
		nf := a.terms.total()
		top := pickTerms(logOdds(a.terms, rest, nf, nall-nf), a.terms, 8, 6)
		var trend []float64
		peakY, peakN := 0, 0
		for y := 2008; y <= d.lastFull; y++ {
			trend = append(trend, float64(a.perYear[y]))
			if a.perYear[y] > peakN {
				peakY, peakN = y, a.perYear[y]
			}
		}
		rows = append(rows, []any{
			d.areaName(a.id),
			ibar{float64(a.pubs) / maxPub, fmtInt(a.pubs)},
			fmtInt(a.projs),
			pct(a.en, a.known),
			fmt.Sprintf("%g", median(a.teams)),
			spark{trend, fmt.Sprintf("%s: publications per year 2008–%d, peak %d (%s)", d.areaName(a.id), d.lastFull, peakY, fmtInt(peakN))},
			strings.Join(top, " · "),
		})
	}
	if other.pubs > 0 {
		rows = append(rows, []any{"All other units", ibar{float64(other.pubs) / maxPub, fmtInt(other.pubs)}, fmtInt(other.projs), "", "", "", ""})
	}
	med := list[0]
	s.Lead = append(s.Lead,
		h(`%s dominates the record with %s of all publications, ahead of %s and %s. The table profiles each faculty and the larger central institutions; the characteristic terms are the title words and phrases most over-represented in an area compared to the rest of the university (log-odds with an informative prior), which makes them a fair sketch of what each area works on.`,
			d.areaName(med.id), pct(med.pubs, len(d.pubs)), d.areaName(list[1].id), d.areaName(list[2].id)))
	s.Blocks = append(s.Blocks, block{
		Title:    "Faculties and institutions at a glance",
		Subtitle: fmt.Sprintf("Publications and projects by main organisational unit; trend shows publications per year 2008–%d, each on its own scale", d.lastFull),
		Figs:     []figure{tableFig{[]string{"Area", "Publications", "Projects", "English", "Median authors", "Trend", "Characteristic title terms"}, rows}},
	})

	s.Blocks = append(s.Blocks, risingTerms(d)...)
	s.Blocks = append(s.Blocks, topicTracker(d), crossAreas(d), sdgs(d))
	return s
}

// termShares returns, for each year from..to, how many titles per thousand
// contain the term.
func termShares(d *data, term string, from, to int, titles map[int]int) []float64 {
	n := make(map[int]int)
	for _, p := range d.pubs {
		if p.Year >= from && p.Year <= to && slices.Contains(p.terms, term) {
			n[p.Year]++
		}
	}
	var out []float64
	for y := from; y <= to; y++ {
		out = append(out, 1000*float64(n[y])/float64(max(titles[y], 1)))
	}
	return out
}

func risingTerms(d *data) []block {
	var (
		a0, a1 = d.lastFull - 10, d.lastFull - 7
		b0, b1 = d.lastFull - 3, d.lastFull
		ca, cb = make(counter), make(counter)
		na, nb int
		titles = make(map[int]int)
	)
	for _, p := range d.pubs {
		titles[p.Year]++
		switch {
		case p.Year >= a0 && p.Year <= a1:
			na++
			for _, t := range p.terms {
				ca[t]++
			}
		case p.Year >= b0 && p.Year <= b1:
			nb++
			for _, t := range p.terms {
				cb[t]++
			}
		}
	}
	both := make(counter)
	for w, c := range ca {
		both[w] += c
	}
	for w, c := range cb {
		both[w] += c
	}
	rise := logOdds(cb, ca, cb.total(), ca.total())
	fall := logOdds(ca, cb, ca.total(), cb.total())
	table := func(ws []string) []figure {
		var rows [][]any
		for _, w := range ws {
			ra, rb := 1000*float64(ca[w])/float64(na), 1000*float64(cb[w])/float64(nb)
			trend := termShares(d, w, 2008, d.lastFull, titles)
			rows = append(rows, []any{w, spark{trend, fmt.Sprintf("%q, titles per 1,000, 2008–%d", w, d.lastFull)}, fmt.Sprintf("%.1f", ra), fmt.Sprintf("%.1f", rb)})
		}
		return []figure{tableFig{[]string{"Term", "2008–" + strconv.Itoa(d.lastFull), fmt.Sprintf("%d–%d", a0, a1), fmt.Sprintf("%d–%d", b0, b1)}, rows}}
	}
	sub := fmt.Sprintf("Title terms per 1,000 publications, %d–%d against %d–%d", a0, a1, b0, b1)
	return []block{
		{Title: "Rising terms", Subtitle: sub, Half: true, Figs: table(pickTerms(rise, both, 25, 12))},
		{Title: "Fading terms", Subtitle: sub, Half: true, Figs: table(pickTerms(fall, both, 25, 12)),
			Notes: []text{h(`Both lists compare the two periods with the same log-odds score as above. As units joined the system at different times, a shift in what is reported can look like a shift in what is researched.`)}},
	}
}

func topicTracker(d *data) block {
	const from = 2008 // the first year with more than a thousand publications
	titles := make(map[int]int)
	hits := make([]map[int]int, len(topics))
	for i := range hits {
		hits[i] = make(map[int]int)
	}
	totals := make([]int, len(topics))
	for _, p := range d.pubs {
		titles[p.Year]++
		t := strings.ToLower(p.Title)
		for i, tp := range topics {
			if tp.Re.MatchString(t) {
				hits[i][p.Year]++
				totals[i]++
			}
		}
	}
	var cards []card
	for i, tp := range topics {
		var (
			vals         []float64
			peakY        int
			peakV, first float64
		)
		for y := from; y <= d.lastFull; y++ {
			v := 1000 * float64(hits[i][y]) / float64(max(titles[y], 1))
			vals = append(vals, v)
			if v > peakV {
				peakY, peakV = y, v
			}
			if y == from {
				first = v
			}
		}
		last := vals[len(vals)-1]
		cards = append(cards, card{
			Kicker: fmt.Sprintf("%s titles", fmtInt(totals[i])),
			Title:  tp.Name,
			Spark:  &spark{vals, fmt.Sprintf("%s: titles per 1,000, %d–%d; peak %d (%.1f)", tp.Name, from, d.lastFull, peakY, peakV)},
			Note:   fmt.Sprintf("%.1f per 1,000 titles in %d, %.1f in %d; peak %d", first, from, last, d.lastFull, peakY),
		})
	}
	return block{
		Title:    "Topic tracker",
		Subtitle: fmt.Sprintf("Share of titles matching a few keywords (in English or German), per 1,000 titles per year, %d–%d", from, d.lastFull),
		Cards:    cards,
	}
}

func crossAreas(d *data) block {
	pairs := make(counter)
	var multi, assigned int
	for _, p := range d.pubs {
		if len(p.areas) == 0 {
			continue
		}
		assigned++
		if len(p.areas) < 2 {
			continue
		}
		multi++
		names := make([]string, len(p.areas))
		for i, a := range p.areas {
			names[i] = d.areaName(a)
		}
		slices.Sort(names)
		for i := range names {
			for j := i + 1; j < len(names); j++ {
				pairs[names[i]+" × "+names[j]]++
			}
		}
	}
	var items []hbar
	for _, kv := range pairs.top(10) {
		items = append(items, hbar{Label: kv.Key, Value: float64(kv.N), Text: fmtInt(kv.N)})
	}
	return block{
		Title:    "Where areas meet",
		Subtitle: fmt.Sprintf("%s of publications (%s) involve more than one faculty or institution; the most frequent pairs", pct(multi, assigned), fmtInt(multi)),
		Half:     true,
		Figs:     []figure{barFig{"Areas", "Publications", items}},
	}
}

func sdgs(d *data) block {
	c := make(counter)
	var with int
	for _, p := range d.projs {
		if len(p.SDG) > 0 {
			with++
		}
		for _, g := range p.SDG {
			label := g.Rep
			if num, rest, ok := strings.Cut(g.Rep, ":"); ok {
				label = num
				if _, en, ok := strings.Cut(rest, " / "); ok {
					label += " · " + strings.TrimSpace(en)
				}
			}
			c[label]++
		}
	}
	var items []hbar
	for _, kv := range c.top(12) {
		items = append(items, hbar{Label: kv.Key, Value: float64(kv.N), Text: fmtInt(kv.N)})
	}
	return block{
		Title:    "Sustainable Development Goals",
		Subtitle: fmt.Sprintf("Projects tagged with a UN SDG; only %s of %s projects carry a tag", fmtInt(with), fmtInt(len(d.projs))),
		Half:     true,
		Figs:     []figure{barFig{"Goal", "Projects", items}},
	}
}

func fundingSection(d *data) section {
	s := section{ID: "funding", Title: "Funding"}
	names := funderNames(d)
	var (
		groups            = make(counter)
		withFunder, joint int
		website           int
		groupProjects     = make(map[string][]*project)
		mixFrom, mixTo    = 2005, d.snapshot.Year()
		mix               = make(map[string]map[int]int)
		firstERC          *project
	)
	for _, g := range funderGroups {
		mix[g] = make(map[int]int)
	}
	for _, p := range d.projs {
		if p.IsJoint {
			joint++
		}
		if strings.TrimSpace(p.Website) != "" {
			website++
		}
		seen := make(map[string]bool)
		for _, f := range p.funders {
			g := funderGroup(f)
			if g == "" || seen[g] {
				continue
			}
			seen[g] = true
			groups[g]++
			groupProjects[g] = append(groupProjects[g], p)
			if y := p.start.Year(); y >= mixFrom && y <= mixTo {
				mix[g][y]++
			}
			if strings.HasPrefix(f, "ERC") && (firstERC == nil || p.start.Before(firstERC.start)) && p.start.Year() >= 1900 {
				firstERC = p
			}
		}
		if len(seen) > 0 {
			withFunder++
		}
	}
	top := groups.top(2)
	s.Lead = append(s.Lead, h(`%s of the projects name at least one funder. Two sources lead by far: %s with %s projects and %s with %s. %s projects (%s) are joint projects with external partners, and %s link to a website of their own.`,
		pct(withFunder, len(d.projs)), top[0].Key, fmtInt(top[0].N), top[1].Key, fmtInt(top[1].N), fmtInt(joint), pct(joint, len(d.projs)), fmtInt(website)))
	if firstERC != nil {
		s.Lead = append(s.Lead, h(`The earliest project funded by the European Research Council in LEURIS started in %d: %s.`,
			firstERC.start.Year(), link(firstERC.Link, firstERC.title())))
	}

	var items []hbar
	for _, kv := range names.top(14) {
		items = append(items, hbar{Label: kv.Key, Value: float64(kv.N), Text: fmtInt(kv.N), Tip: fmt.Sprintf("%s: %s projects (%s)", kv.Key, fmtInt(kv.N), funderGroup(kv.Key))})
	}
	s.Blocks = append(s.Blocks, block{
		Title: "Funders", Subtitle: "Projects per funding body, as named in LEURIS", Half: true, Figs: []figure{barFig{"Funder", "Projects", items}},
	})

	var rows [][]any
	for _, kv := range groups.top(0) {
		var ds []float64
		for _, p := range groupProjects[kv.Key] {
			if p.plausible() {
				ds = append(ds, years(p.start, p.end))
			}
		}
		rows = append(rows, []any{kv.Key, fmtInt(kv.N), fmtYears(median(ds)), fmtYears(quantile(ds, .9))})
	}
	s.Blocks = append(s.Blocks, block{
		Title: "Typical project length", Subtitle: "By funder group, for projects with plausible dates", Half: true,
		Figs: []figure{tableFig{[]string{"Funder group", "Projects", "Median length", "90th percentile"}, rows}},
	})

	xs := yearAxis(mixFrom, mixTo)
	var ss []series
	for _, g := range funderGroups {
		var vals []float64
		for y := mixFrom; y <= mixTo; y++ {
			vals = append(vals, float64(mix[g][y]))
		}
		ss = append(ss, series{g, vals})
	}
	s.Blocks = append(s.Blocks, block{
		Title:    "Funding mix over time",
		Subtitle: fmt.Sprintf("Share of funder groups among projects starting each year, %d–%d; a project with several funder groups counts for each", mixFrom, mixTo),
		Figs:     []figure{stackFig{"Funding mix over time", "Start year", "project–funder pairs", xs, ss}},
	})
	return s
}

func publishing(d *data) section {
	s := section{ID: "publishing", Title: "How Leipzig publishes"}
	var (
		types               = make(counter)
		langs               = make(counter)
		journals            = make(map[string]counter) // key to spellings
		from                = 2000
		perYear, en, de, kn = make(map[int]int), make(map[int]int), make(map[int]int), make(map[int]int)
		doi                 = make(map[int]int)
		teams               = make(map[int][]float64)
	)
	for _, p := range d.pubs {
		types[p.Type]++
		if p.lang != "" && p.lang != "Other" && p.lang != "Multilingual" {
			langs[p.lang]++
		}
		if p.Journal != "" {
			k := journalKey(p.Journal)
			if journals[k] == nil {
				journals[k] = make(counter)
			}
			journals[k][strings.TrimSpace(p.Journal)]++
		}
		perYear[p.Year]++
		if p.hasDOI {
			doi[p.Year]++
		}
		teams[p.Year] = append(teams[p.Year], float64(len(p.Authors)))
		if p.lang != "" {
			kn[p.Year]++
			switch p.lang {
			case "English":
				en[p.Year]++
			case "German":
				de[p.Year]++
			}
		}
	}

	var items []hbar
	for _, kv := range types.top(0) {
		name := cmp.Or(typeEN[kv.Key], kv.Key)
		items = append(items, hbar{Label: name, Value: float64(kv.N), Text: fmtInt(kv.N), Tip: fmt.Sprintf("%s: %s (%s)", name, fmtInt(kv.N), pct(kv.N, len(d.pubs)))})
	}
	s.Blocks = append(s.Blocks, block{Title: "Publication types", Subtitle: "All publications by type", Half: true, Figs: []figure{barFig{"Type", "Publications", items}}})

	type journal struct {
		name string
		n    int
	}
	var js []journal
	for _, spellings := range journals {
		js = append(js, journal{spellings.top(1)[0].Key, spellings.total()})
	}
	slices.SortFunc(js, func(a, b journal) int { return cmp.Or(cmp.Compare(b.n, a.n), cmp.Compare(a.name, b.name)) })
	items = nil
	for _, j := range js[:min(14, len(js))] {
		items = append(items, hbar{Label: j.name, Value: float64(j.n), Text: fmtInt(j.n)})
	}
	s.Blocks = append(s.Blocks, block{Title: "Most frequent journals", Subtitle: fmt.Sprintf("Spellings merged; %s distinct journals in all", fmtInt(len(js))), Half: true, Figs: []figure{barFig{"Journal", "Publications", items}}})

	var (
		xs                   = yearAxis(from, d.lastFull)
		enS, deS, mean, medS []float64
		doiS                 []float64
	)
	share := func(a, b int) float64 {
		if b == 0 {
			return math.NaN()
		}
		return float64(a) / float64(b)
	}
	for y := from; y <= d.lastFull; y++ {
		enS = append(enS, share(en[y], kn[y]))
		deS = append(deS, share(de[y], kn[y]))
		doiS = append(doiS, share(doi[y], perYear[y]))
		var sum float64
		for _, t := range teams[y] {
			sum += t
		}
		mean = append(mean, sum/float64(max(len(teams[y]), 1)))
		medS = append(medS, median(teams[y]))
	}
	pctFmt := func(v float64) string { return fmtPct(v) }
	oneDec := func(v float64) string { return fmt.Sprintf("%.1f", v) }
	idx := func(y int) int { return y - from }
	s.Lead = append(s.Lead,
		h(`Leipzig publishes mostly in journals, and increasingly in English: the English share of publications with a known language grew from %s in %d to %s in %d. Records name %d languages in all.`,
			fmtPct(enS[idx(2005)]), 2005, fmtPct(enS[idx(d.lastFull)]), d.lastFull, len(langs)),
		h(`Teams grow, too. The average publication had %.1f authors in %d and %.1f in %d; the median moved from %g to %g, so the rise is not only driven by a few mega-consortia.`,
			mean[idx(2008)], 2008, mean[idx(d.lastFull)], d.lastFull, medS[idx(2008)], medS[idx(d.lastFull)]))
	s.Blocks = append(s.Blocks,
		block{
			Title:    "Language",
			Subtitle: fmt.Sprintf("Share of publications in English and German, among those with a known language, %d–%d", from, d.lastFull),
			Figs:     []figure{lineFig{"Language of publications", "Year", xs, []series{{"English", enS}, {"German", deS}}, true, pctFmt}},
		},
		block{
			Title:    "Team size",
			Subtitle: fmt.Sprintf("Authors per publication, %d–%d", from, d.lastFull),
			Figs:     []figure{lineFig{"Authors per publication", "Year", xs, []series{{"Mean", mean}, {"Median", medS}}, false, oneDec}},
		},
		block{
			Title:    "Publications with a DOI",
			Subtitle: fmt.Sprintf("Share of publications with a DOI recorded in LEURIS, %d–%d", from, d.lastFull),
			Figs:     []figure{lineFig{"Share of publications with a DOI", "Year", xs, []series{{"With DOI", doiS}}, true, pctFmt}},
		})
	return s
}

func people(d *data) section {
	s := section{ID: "people", Title: "People and collaboration"}
	var (
		names      = make(map[string]string) // fisPersid to name
		pubCount   = make(counter)
		pubAreas   = make(map[string]counter)
		projCount  = make(counter)
		pairs      = make(counter)
		orcid      = make(map[string]bool)
		external   int
		internal   []float64
		researcher = make(map[string]bool)
	)
	for _, p := range d.pubs {
		var ids []string
		ext := false
		for _, a := range p.Authors {
			if a.FisPersid == "" {
				ext = true
				continue
			}
			if slices.Contains(ids, a.FisPersid) {
				continue
			}
			ids = append(ids, a.FisPersid)
			researcher[a.FisPersid] = true
			if names[a.FisPersid] == "" {
				names[a.FisPersid] = a.FullName
			}
			if a.Orcid != "" {
				orcid[a.FisPersid] = true
			}
			pubCount[a.FisPersid]++
			if pubAreas[a.FisPersid] == nil {
				pubAreas[a.FisPersid] = make(counter)
			}
			pubAreas[a.FisPersid][p.area]++
		}
		if ext {
			external++
		}
		internal = append(internal, float64(len(ids)))
		if len(ids) <= 40 {
			slices.Sort(ids)
			for i := range ids {
				for j := i + 1; j < len(ids); j++ {
					pairs[ids[i]+"|"+ids[j]]++
				}
			}
		}
	}
	type team struct {
		p *project
		n int
	}
	var teams []team
	for _, p := range d.projs {
		ppl := p.people()
		teams = append(teams, team{p, len(ppl)})
		lead := make(map[string]bool)
		for _, a := range p.Applicants {
			lead[a.FisPersid] = true
		}
		for _, m := range p.Managers {
			lead[m.Person.FisPersid] = true
		}
		for id := range lead {
			if id != "" {
				projCount[id]++
			}
		}
		for _, a := range ppl {
			if a.FisPersid == "" {
				continue
			}
			researcher[a.FisPersid] = true
			if names[a.FisPersid] == "" {
				names[a.FisPersid] = a.FullName
			}
			if a.Orcid != "" {
				orcid[a.FisPersid] = true
			}
		}
	}
	s.Tiles = []tile{
		{"Researchers", fmtInt(len(researcher)), "university members in the data"},
		{"With ORCID", pct(len(orcid), len(researcher)), fmtInt(len(orcid)) + " researchers have an ORCID iD on record"},
		{"With external co-authors", pct(external, len(d.pubs)), "of publications name someone from outside the university"},
		{"Internal authors", fmt.Sprintf("%g", median(internal)), "median university members per publication"},
	}
	s.Lead = append(s.Lead, h(`Most publications are collaborations beyond Leipzig: %s name at least one author from outside the university. Only university members are identified across records, so the lists below cover them alone.`,
		pct(external, len(d.pubs))))

	var items []hbar
	for _, kv := range pubCount.top(12) {
		area := d.areaName(pubAreas[kv.Key].top(1)[0].Key)
		items = append(items, hbar{Label: names[kv.Key], Note: area, Value: float64(kv.N), Text: fmtInt(kv.N)})
	}
	s.Blocks = append(s.Blocks, block{Title: "Most publications", Subtitle: "Publications per researcher, with their main area", Half: true, Figs: []figure{barFig{"Researcher", "Publications", items}}})
	items = nil
	for _, kv := range projCount.top(12) {
		items = append(items, hbar{Label: names[kv.Key], Value: float64(kv.N), Text: fmtInt(kv.N)})
	}
	s.Blocks = append(s.Blocks, block{Title: "Most projects", Subtitle: "Projects per researcher as applicant or manager", Half: true, Figs: []figure{barFig{"Researcher", "Projects", items}}})

	var rows [][]any
	for _, kv := range pairs.top(10) {
		a, b, _ := strings.Cut(kv.Key, "|")
		rows = append(rows, []any{names[a] + " & " + names[b], fmtInt(kv.N)})
	}
	s.Blocks = append(s.Blocks, block{Title: "Closest collaborators", Subtitle: "Pairs of university members with the most joint publications", Half: true,
		Figs: []figure{tableFig{[]string{"Pair", "Joint publications"}, rows}}})

	slices.SortFunc(teams, func(a, b team) int { return cmp.Or(cmp.Compare(b.n, a.n), cmp.Compare(a.p.ID, b.p.ID)) })
	rows = nil
	for _, t := range teams[:8] {
		rows = append(rows, []any{link(t.p.Link, shorten(t.p.title(), 80)), fmtInt(t.n)})
	}
	s.Blocks = append(s.Blocks, block{Title: "Largest project teams", Subtitle: "Applicants, collaborators and managers named on a project", Half: true,
		Figs: []figure{tableFig{[]string{"Project", "People"}, rows}}})
	return s
}

func curiosities(d *data) section {
	s := section{ID: "curiosities", Title: "Curiosities"}
	s.Lead = append(s.Lead, h(`Every large database has its oddities. Some of these are data entry slips, some are simply remarkable.`))
	var cards []card
	add := func(kicker, title string, body text) {
		cards = append(cards, card{Kicker: kicker, Title: title, Body: body})
	}

	// Dates.
	var (
		oldest, longest, latest, reversed *project
		nReversed, nZero, nFar            int
		farEnds                           = make(counter)
		untitled                          int
	)
	horizon := d.snapshot.AddDate(10, 0, 0)
	for _, p := range d.projs {
		if strings.TrimSpace(p.TitleDe) == "" && strings.TrimSpace(p.TitleEn) == "" {
			untitled++
		}
		if p.start.IsZero() {
			continue
		}
		if oldest == nil || p.start.Before(oldest.start) {
			oldest = p
		}
		if p.end.IsZero() {
			continue
		}
		switch {
		case p.end.Before(p.start):
			nReversed++
			if reversed == nil || p.start.Sub(p.end) > reversed.start.Sub(reversed.end) {
				reversed = p
			}
		case p.end.Equal(p.start):
			nZero++
		}
		if p.plausible() && (longest == nil || years(p.start, p.end) > years(longest.start, longest.end)) {
			longest = p
		}
		if latest == nil || p.end.After(latest.end) {
			latest = p
		}
		if p.end.After(horizon) {
			nFar++
			farEnds[p.end.Format(time.DateOnly)]++
		}
	}
	if oldest != nil && oldest.start.Year() < 1900 {
		add("Projects", "A project from antiquity",
			h(`%s officially started on %s and ended on %s, a run of %s years. A typo, presumably.`,
				link(oldest.Link, shorten(oldest.title(), 90)), date(oldest.start), date(oldest.end), fmtInt(int(years(oldest.start, oldest.end)))))
	}
	if longest != nil {
		add("Projects", "The longest-running project",
			h(`%s runs from %d to %d: %s years.`,
				link(longest.Link, longest.title()), longest.start.Year(), longest.end.Year(), fmtInt(int(math.Round(years(longest.start, longest.end))))))
	}
	if reversed != nil {
		add("Projects", "Ending before they begin",
			h(`%d projects end before they start. The record holder, %s, finishes %s days before it begins.`,
				nReversed, link(reversed.Link, shorten(reversed.title(), 90)), fmtInt(int(reversed.start.Sub(reversed.end).Hours()/24))))
	}
	if nFar > 0 && latest != nil {
		day := farEnds.top(1)[0]
		dt, _ := time.Parse(time.DateOnly, day.Key)
		body := h(`%d projects are scheduled to end more than ten years from now. The last one, %s, ends on %s.`,
			nFar, link(latest.Link, shorten(latest.title(), 90)), date(latest.end))
		if day.N > 1 {
			body = h(`%d projects are scheduled to end more than ten years from now. The last one, %s, ends on %s. %d of them share the end date %s, which looks like a placeholder.`,
				nFar, link(latest.Link, shorten(latest.title(), 90)), date(latest.end), day.N, date(dt))
		}
		add("Projects", "Planning ahead", body)
	}
	if nZero > 0 || untitled > 0 {
		add("Projects", "Blink and you miss it",
			h(`%s start and end on the same day, and %s no title at all.`,
				plural(nZero, "project", "projects"), plural(untitled, "has", "have")))
	}

	// Publications.
	var (
		most, longTitle *publication
		shortTitles     []string
		titleCount      = make(counter)
		questions       int
		qByArea         = make(counter)
		nByArea         = make(counter)
		exclaim         *publication
		nExclaim        int
		leipzig, saxony int
		oldestPub       *publication
	)
	for _, p := range d.pubs {
		t := strings.TrimSpace(p.Title)
		if most == nil || len(p.Authors) > len(most.Authors) {
			most = p
		}
		if longTitle == nil || utf8.RuneCountInString(t) > utf8.RuneCountInString(longTitle.Title) {
			longTitle = p
		}
		if n := utf8.RuneCountInString(t); n > 0 && n <= 4 && !slices.Contains(shortTitles, t) {
			shortTitles = append(shortTitles, t)
		}
		titleCount[t]++
		nByArea[p.area]++
		if strings.HasSuffix(t, "?") {
			questions++
			qByArea[p.area]++
		}
		if strings.Contains(t, "!") {
			nExclaim++
			if n := utf8.RuneCountInString(t); n >= 25 && (exclaim == nil || n < utf8.RuneCountInString(exclaim.Title)) {
				exclaim = p
			}
		}
		lt := strings.ToLower(t)
		if strings.Contains(lt, "leipzig") {
			leipzig++
		}
		if strings.Contains(lt, "saxony") || strings.Contains(lt, "sachsen") || strings.Contains(lt, "sächsisch") {
			saxony++
		}
		if p.Year > 0 && (oldestPub == nil || p.Year < oldestPub.Year) {
			oldestPub = p
		}
	}
	add("Publications", "Safety in numbers",
		h(`The longest author list belongs to %s (%d), with %s authors.`, link(most.Link, most.Title), most.Year, fmtInt(len(most.Authors))))
	add("Publications", "Brevity and its opposite",
		h(`The longest title runs to %s characters (%s), the shortest are just %s.`,
			fmtInt(utf8.RuneCountInString(longTitle.Title)), link(longTitle.Link, shorten(longTitle.Title, 60)), quoteList(shortTitles, 8)))
	var common []string
	for _, kv := range titleCount.top(4) {
		common = append(common, fmt.Sprintf("“%s” (%d)", kv.Key, kv.N))
	}
	add("Publications", "The most popular title",
		h(`Titles are not unique. The most common ones are %s.`, strings.Join(common, ", ")))
	var bestQ string
	var bestQShare float64
	for a, n := range nByArea {
		if n >= 500 && float64(qByArea[a])/float64(n) > bestQShare {
			bestQ, bestQShare = a, float64(qByArea[a])/float64(n)
		}
	}
	add("Publications", "Asking questions",
		h(`%s titles (%s) end in a question mark. %s asks the most: %s of its titles.`,
			fmtInt(questions), pct(questions, len(d.pubs)), d.areaName(bestQ), fmtPct(bestQShare)))
	if exclaim != nil {
		add("Publications", "Exclamation marks",
			h(`%d titles contain an exclamation mark, as in %s.`, nExclaim, link(exclaim.Link, "“"+exclaim.Title+"”")))
	}
	add("Publications", "Home turf",
		h(`%s titles mention Leipzig and %s mention Saxony.`, fmtInt(leipzig), fmtInt(saxony)))
	if oldestPub != nil {
		add("Publications", "The oldest record",
			h(`The earliest publication in LEURIS dates from %d: %s.`, oldestPub.Year, link(oldestPub.Link, oldestPub.Title)))
	}

	// Metadata.
	raw := make(map[string]map[string]bool)
	spellings := make(map[string]bool)
	for _, p := range d.pubs {
		if p.Language == "" {
			continue
		}
		spellings[p.Language] = true
		if raw[p.lang] == nil {
			raw[p.lang] = make(map[string]bool)
		}
		raw[p.lang][p.Language] = true
	}
	var en []string
	for v := range raw["English"] {
		en = append(en, v)
	}
	slices.Sort(en)
	add("Metadata", fmt.Sprintf("%d ways to name a language", len(spellings)),
		h(`The language field is free text. English alone is spelled %s.`, quoteList(en, 12)))
	langCount := make(counter)
	for _, p := range d.pubs {
		if p.lang != "" && p.lang != "Other" && p.lang != "Multilingual" {
			langCount[p.lang]++
		}
	}
	var (
		rare  []string
		langs = langCount.top(0)
	)
	slices.Reverse(langs)
	for _, kv := range langs[:min(10, len(langs))] {
		rare = append(rare, fmt.Sprintf("%s (%d)", kv.Key, kv.N))
	}
	if len(langs) > 2 {
		add("Metadata", "Small languages",
			h(`Besides English and German, %d languages appear. The rarest: %s.`, len(langs)-2, strings.Join(rare, ", ")))
	}
	var weekend, created int
	for _, p := range d.pubs {
		if p.created.IsZero() {
			continue
		}
		created++
		if wd := p.created.Weekday(); wd == time.Saturday || wd == time.Sunday {
			weekend++
		}
	}
	add("Metadata", "Weekend shifts",
		h(`%s of publication records were created on a Saturday or Sunday, %s records in all.`, pct(weekend, created), fmtInt(weekend)))
	if name := softHyphenated(d); name != "" {
		add("Metadata", "An invisible character",
			h(`The name of the funder “%s” contains a soft hyphen (U+00AD): invisible on screen, but enough to break an exact search.`, name))
	}
	slices.SortStableFunc(cards, func(a, b card) int { return cmp.Compare(kickerOrder(a.Kicker), kickerOrder(b.Kicker)) })
	s.Blocks = append(s.Blocks, block{Cards: cards})
	return s
}

// softHyphenated returns the first funder name with a soft hyphen in it.
func softHyphenated(d *data) string {
	for _, p := range d.projs {
		for _, f := range p.Funding {
			if strings.Contains(f.Agency.Name, "\u00ad") {
				return cleanName(f.Agency.Name)
			}
		}
	}
	return ""
}

func kickerOrder(k string) int {
	return slices.Index([]string{"Projects", "Publications", "Metadata"}, k)
}

func quoteList(xs []string, n int) string {
	var q []string
	for _, x := range xs[:min(n, len(xs))] {
		q = append(q, "“"+x+"”")
	}
	s := strings.Join(q, ", ")
	if len(xs) > n {
		s += " and others"
	}
	return s
}

func notes(d *data) []text {
	return []text{
		h(`Source: the LEURIS GraphQL API of Leipzig University, fetched with leurisfetch for the whole university (organisational unit 1). Latest change in the data: %s.`, date(d.snapshot)),
		h(`LEURIS shows what faculties and institutes enter. Coverage differs between units and over time, especially before the system went live in 2017, so counts describe the record, not necessarily the research output itself.`),
		h(`Dates are read in Europe/Berlin time, as the API stores local midnight in UTC. Areas are the faculty or central institution above a record's main organisational unit. Languages, journal titles and funder names are normalized; publication terms come from titles only.`),
	}
}
