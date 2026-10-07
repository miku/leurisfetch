package main

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// data holds the loaded records together with lookups shared by all sections.
type data struct {
	pubs     []*publication
	projs    []*project
	units    map[string]string // organisational unit id to German name
	snapshot time.Time         // most recent update in the data, stands in for "now"
	lastFull int               // last complete publication year
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.In(berlin)
}

func prepare(pubs []*publication, projs []*project) *data {
	d := &data{pubs: pubs, projs: projs, units: make(map[string]string)}
	addUnits := func(main *unit, more []unit) {
		if main != nil {
			d.units[main.ID] = main.TitleDe
		}
		for _, u := range more {
			d.units[u.ID] = u.TitleDe
		}
	}
	seen := func(t time.Time) {
		if t.After(d.snapshot) {
			d.snapshot = t
		}
	}
	for _, p := range pubs {
		addUnits(p.Unit, p.MoreUnits)
		p.created = parseTime(p.Created)
		seen(parseTime(p.Updated))
		p.lang = normLang(p.Language)
		p.terms = terms(p.Title)
		for _, id := range p.Identifiers {
			if id.Type.Key == "DOI" {
				p.hasDOI = true
			}
		}
	}
	for _, p := range projs {
		addUnits(p.Unit, p.MoreUnits)
		p.created = parseTime(p.Created)
		seen(parseTime(p.Updated))
		p.start, p.end = parseTime(p.StartDate), parseTime(p.EndDate)
		for _, f := range p.Funding {
			if name := cleanName(f.Agency.Name); name != "" && !slices.Contains(p.funders, name) {
				p.funders = append(p.funders, name)
			}
		}
	}
	// Areas need all unit names, so they are resolved in a second pass.
	for _, p := range pubs {
		p.area = d.areaOf(p.Unit)
		if p.area != "" {
			p.areas = append(p.areas, p.area)
		}
		for _, u := range p.MoreUnits {
			if a := d.areaOf(&u); a != "" && !slices.Contains(p.areas, a) {
				p.areas = append(p.areas, a)
			}
		}
	}
	for _, p := range projs {
		p.area = d.areaOf(p.Unit)
	}
	d.lastFull = d.snapshot.Year() - 1
	return d
}

// areaOf maps a unit to its top level area: a faculty (children of 1-3
// "Fakultäten") or a central institution (children of 1-719).
func (d *data) areaOf(u *unit) string {
	if u == nil || u.Path == "" {
		return ""
	}
	segs := strings.Split(u.Path, "-")
	if len(segs) >= 3 {
		return segs[2]
	}
	return segs[len(segs)-1]
}

func (d *data) areaName(id string) string {
	if id == "" {
		return "Unassigned"
	}
	name := d.units[id]
	if en, ok := areaEN[name]; ok {
		return en
	}
	if name == "" {
		return "Unit " + id
	}
	return name
}

// areaEN gives short English names for the faculties and the larger central
// institutions; the API only returns German names.
var areaEN = map[string]string{
	"Medizinische Fakultät und Universitätsklinikum AöR":         "Medicine",
	"Fakultät für Geschichte, Kunst- und Regionalwissenschaften": "History, Arts & Area Studies",
	"Fakultät für Sozialwissenschaften und Philosophie":          "Social Sciences & Philosophy",
	"Philologische Fakultät":                                     "Philology",
	"Fakultät für Physik und Erdsystemwissenschaften":            "Physics & Earth System Sciences",
	"Fakultät für Lebenswissenschaften":                          "Life Sciences",
	"Veterinärmedizinische Fakultät":                             "Veterinary Medicine",
	"Wirtschaftswissenschaftliche Fakultät":                      "Economics & Management",
	"Fakultät für Mathematik und Informatik":                     "Mathematics & Computer Science",
	"Fakultät für Chemie":                                        "Chemistry & Mineralogy",
	"Fakultät für Chemie und Mineralogie":                        "Chemistry & Mineralogy",
	"Juristische Fakultät":                                       "Law",
	"Erziehungswissenschaftliche Fakultät":                       "Education",
	"Theologische Fakultät":                                      "Theology",
	"Sportwissenschaftliche Fakultät":                            "Sport Science",
	"German Centre for Integrative Biodiversity Research (iDiv)": "iDiv – Biodiversity Research",
	"Leipzig Research Centre Global Dynamics (ReCentGlobe)":      "ReCentGlobe – Global Dynamics",
	"Universitätsbibliothek (UB)":                                "University Library",
	"Universitätsrechenzentrum (URZ)":                            "Computing Centre",
	"Interdisziplinäres Zentrum für Bioinformatik (IZBI)":        "Bioinformatics Centre (IZBI)",
	"Biotechnologisch-Biomedizinisches Zentrum (BBZ)":            "Biotech-Biomedical Centre (BBZ)",
	"Zentrum für Lehrerbildung und Schulforschung (ZLS)":         "Teacher Education Centre (ZLS)",
}

// normLang folds the free text language field ("eng", "Englisch", "English ",
// ...) into a language name.
func normLang(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	if strings.ContainsAny(s, ",;/&+") || strings.Contains(s, " und ") || strings.Contains(s, " and ") {
		return "Multilingual"
	}
	if l, ok := languages[s]; ok {
		return l
	}
	return "Other"
}

var languages = map[string]string{}

func init() {
	for name, spellings := range map[string]string{
		"English":       "eng en english englisch",
		"German":        "ger deu de german deutsch",
		"Spanish":       "spa es spanish spanisch",
		"French":        "fre fra fr french französisch",
		"Portuguese":    "por pt portuguese portugiesisch",
		"Italian":       "ita it italian italienisch",
		"Polish":        "pol pl polish polnisch",
		"Russian":       "rus ru russian russisch",
		"Chinese":       "chi zho zh chinese chinesisch",
		"Czech":         "cze ces cs czech tschechisch",
		"Japanese":      "jpn ja japanese japanisch",
		"Dutch":         "dut nld nl dutch niederländisch",
		"Upper Sorbian": "hsb obersorbisch",
		"Lower Sorbian": "dsb niedersorbisch",
		"Catalan":       "cat catalan katalanisch",
		"Galician":      "glg galician galicisch",
		"Esperanto":     "epo esperanto",
		"Arabic":        "ara arabic arabisch",
		"Greek":         "gre ell greek griechisch",
		"Slovenian":     "slv slovenian slowenisch",
		"Hungarian":     "hun hungarian ungarisch",
		"Serbian":       "srp serbian serbisch",
		"Danish":        "dan danish dänisch",
		"Ukrainian":     "ukr ukrainian ukrainisch",
		"Korean":        "kor korean koreanisch",
		"Swahili":       "swa swahili",
		"Old English":   "ang",
		"Welsh":         "wel cym welsh walisisch",
		"Latin":         "lat latin latein lateinisch",
		"Turkish":       "tur turkish türkisch",
		"Swedish":       "swe swedish schwedisch",
	} {
		for _, sp := range strings.Fields(spellings) {
			languages[sp] = name
		}
	}
}

// cleanName removes soft hyphens and surrounding space from agency names.
func cleanName(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\u00ad", "")), " ")
}

// funderGroup folds the funding agencies into a handful of groups that fit
// the categorical palette.
func funderGroup(name string) string {
	switch n := strings.ToLower(name); {
	case strings.HasPrefix(n, "dfg"):
		return "DFG"
	case strings.HasPrefix(n, "eu ") || strings.HasPrefix(n, "erc"):
		return "EU & ERC"
	case strings.HasPrefix(n, "bm") || strings.Contains(n, "bundesministeri"):
		return "Federal ministries"
	case strings.HasPrefix(n, "haushaltsmittel"):
		return "University budget"
	case strings.HasPrefix(n, "smw") || strings.Contains(n, "sächsische ministerien"):
		return "Saxon ministries"
	case strings.HasPrefix(n, "stiftung") || strings.HasPrefix(n, "fördergesellschaft") || strings.HasPrefix(n, "vereine"):
		return "Foundations & societies"
	case n == "wirtschaft" || strings.HasPrefix(n, "sächsische wirtschaft") || strings.HasPrefix(n, "aif") || strings.HasPrefix(n, "sonstige private"):
		return "Industry & private"
	case n == "keine drittmittel":
		return ""
	}
	return "Other"
}

var funderGroups = []string{"DFG", "University budget", "Federal ministries", "EU & ERC", "Foundations & societies", "Industry & private", "Saxon ministries", "Other"}

var typeEN = map[string]string{
	"ARTICLE":                  "Journal article",
	"CHAPTER_IN_EDITED_VOLUME": "Chapter in edited volume",
	"CHAPTER_IN_PROCEEDINGS":   "Conference paper",
	"BOOK":                     "Monograph",
	"MISC":                     "Miscellaneous",
	"EDITED_VOLUME":            "Edited volume",
	"CHAPTER_IN_BOOK":          "Chapter in book",
	"NON_ACADEMIC":             "Non-academic media",
	"REPORT":                   "Report",
	"WORKING_PAPER":            "Working paper",
	"PROCEEDINGS":              "Proceedings volume",
	"SPECIAL_ISSUE":            "Special issue",
	"DOCTORAL_THESIS":          "Doctoral thesis",
	"SOURCE_EDITION":           "Source edition",
	"HABILITATION_THESIS":      "Habilitation thesis",
	"GUIDELINE":                "Guideline",
}

// counter counts strings.
type counter map[string]int

type kv struct {
	Key string
	N   int
}

// top returns the n most frequent keys, ties broken alphabetically; n <= 0
// returns all.
func (c counter) top(n int) []kv {
	out := make([]kv, 0, len(c))
	for k, v := range c {
		out = append(out, kv{k, v})
	}
	slices.SortFunc(out, func(a, b kv) int {
		return cmp.Or(cmp.Compare(b.N, a.N), cmp.Compare(a.Key, b.Key))
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

func (c counter) total() (t int) {
	for _, v := range c {
		t += v
	}
	return t
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

func quantile(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	return s[int(math.Round(q*float64(len(s)-1)))]
}

// fmtInt formats n with thousands separators.
func fmtInt(n int) string {
	s := fmt.Sprint(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func pct(part, whole int) string {
	if whole == 0 {
		return "–"
	}
	return fmtPct(float64(part) / float64(whole))
}

// fmtPct formats a share, with a decimal only where it matters.
func fmtPct(f float64) string {
	switch {
	case math.IsNaN(f):
		return "–"
	case f > 0 && f < 0.01:
		return fmt.Sprintf("%.1f%%", f*100)
	}
	return fmt.Sprintf("%.0f%%", f*100)
}

func fmtYears(f float64) string {
	if f < 1 {
		return fmt.Sprintf("%.0f months", f*12)
	}
	return fmt.Sprintf("%.1f years", f)
}

// fracYear places t on a continuous year axis.
func fracYear(t time.Time) float64 {
	return float64(t.Year()) + float64(t.YearDay()-1)/365.25
}

// years is the time between two dates in years; unlike time.Duration, it
// does not overflow after 292 years.
func years(from, to time.Time) float64 {
	return fracYear(to) - fracYear(from)
}

func date(t time.Time) string {
	return fmt.Sprintf("%d %s %d", t.Day(), t.Month(), t.Year())
}

// plural returns "1 project" or "2 projects".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmtInt(n) + " " + many
}

// shorten cuts s to at most n runes, at a word boundary if possible.
func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if i := strings.LastIndex(cut, " "); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:-–") + "…"
}
