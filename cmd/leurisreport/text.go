package main

import (
	"cmp"
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	tagRe = regexp.MustCompile(`<[^>]*>`)
	// genderRe matches gender-inclusive endings like Lehrer*innen or
	// Schüler:innen, which would otherwise yield the term "innen".
	genderRe = regexp.MustCompile(`(?i)(\pL)[*:_/](innen)\b`)
)

// terms returns the distinct unigrams and bigrams of a title, lowercased and
// without stopwords. Bigrams span neither stopwords nor punctuation, so "heart
// failure" is a bigram, while "quality of life" and "Leipzig: heart" are not.
func terms(title string) []string {
	title = strings.ToLower(genderRe.ReplaceAllString(tagRe.ReplaceAllString(title, " "), "$1$2"))
	var (
		out  []string
		seen = make(map[string]bool)
	)
	add := func(t string) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	isBreak := func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsSpace(r) && r != '-'
	}
	for _, chunk := range strings.FieldsFunc(title, isBreak) {
		prev := ""
		for _, w := range strings.Fields(chunk) {
			w = strings.Trim(w, "-")
			if !isTerm(w) {
				prev = ""
				continue
			}
			add(w)
			if prev != "" {
				add(prev + " " + w)
			}
			prev = w
		}
	}
	return out
}

func isTerm(w string) bool {
	if utf8.RuneCountInString(w) < 3 || stopwords[w] {
		return false
	}
	return strings.IndexFunc(w, unicode.IsLetter) >= 0
}

var stopwords = make(map[string]bool)

func init() {
	for _, w := range strings.Fields(stopwordList) {
		stopwords[w] = true
	}
}

// stopwordList covers English and German function words, some French,
// Spanish and Italian ones, and words that only structure titles.
const stopwordList = `
a about above after again against all also am an and any are as at be because
been before being below between both but by can could did do does doing down
during each few for from further had has have having here how if in into is it
its itself just more most no nor not now of off on once only or other our out
over own same she should so some such than that the their them then there
these they this those through to too under until up upon very was we were
what when where which while who whom why will with within without would you
your using use used based via versus new among toward towards across one two
three part vol volume eds iii study studies results analysis effect effects
role approach

aber alle allem allen aller alles als also am an andere anderen anderer auch
auf aus bei beim bis da damit dann das dass daß dem den denen der des die dies
diese diesem diesen dieser dieses doch dort durch ein eine einem einen einer
eines einige einiger er es etwa für gegen hat hatte ihr ihre ihren ihrer im
in ins ist jedoch kann kein keine mit nach neue neuen neuer neues nicht noch
nur ob oder ohne seine seiner sich sie sind so sowie über um und uns unter vom
von vor wann war was weiter welche wenn werden wie wird wir zu zum zur zwischen
teil band bd hrsg beitrag beiträge

les des une dans pour par sur aux avec del los las por con una della degli
delle per nel
`

// logOdds scores how characteristic each term of fg is compared to bg, with
// nf and nb the total counts of each: z-scores of the log-odds ratio with an
// informative Dirichlet prior (Monroe, Colaresi & Quinn 2008, "Fightin'
// Words"). Unlike plain frequency ratios, rare terms do not dominate. Only
// the terms of fg are scored, so bg may be partial.
func logOdds(fg, bg counter, nf, nb int) map[string]float64 {
	const a0 = 500.0 // prior strength in pseudo counts
	var (
		f, b   = float64(nf), float64(nb)
		scores = make(map[string]float64, len(fg))
	)
	for w := range fg {
		yi, yj := float64(fg[w]), float64(bg[w])
		aw := a0 * (yi + yj) / (f + b)
		d := math.Log((yi+aw)/(f+a0-yi-aw)) - math.Log((yj+aw)/(b+a0-yj-aw))
		scores[w] = d / math.Sqrt(1/(yi+aw)+1/(yj+aw))
	}
	return scores
}

// pickTerms returns up to k terms with the highest positive score among those
// with at least minCount occurrences. A unigram is replaced by the bigram it
// mostly occurs in ("fibrillation" by "atrial fibrillation"), and terms that
// share a word with an earlier pick are skipped, to avoid near duplicates.
func pickTerms(scores map[string]float64, counts counter, minCount, k int) []string {
	type cand struct {
		w string
		s float64
	}
	var cands []cand
	bestBigram := make(map[string]string) // word to its most frequent bigram
	for w, s := range scores {
		if s <= 0 || counts[w] < minCount {
			continue
		}
		cands = append(cands, cand{w, s})
		if a, b, ok := strings.Cut(w, " "); ok {
			for _, t := range []string{a, b} {
				if cur, ok := bestBigram[t]; !ok || counts[w] > counts[cur] || (counts[w] == counts[cur] && w < cur) {
					bestBigram[t] = w
				}
			}
		}
	}
	slices.SortFunc(cands, func(a, b cand) int {
		return cmp.Or(cmp.Compare(b.s, a.s), cmp.Compare(a.w, b.w))
	})
	var (
		out  []string
		used = make(map[string]bool)
	)
	for _, c := range cands {
		w := c.w
		if !strings.Contains(w, " ") {
			if bg, ok := bestBigram[w]; ok && counts[bg]*2 >= counts[w] {
				w = bg
			}
		}
		words := strings.Fields(w)
		if slices.ContainsFunc(words, func(t string) bool { return used[t] }) {
			continue
		}
		for _, t := range words {
			used[t] = true
		}
		out = append(out, w)
		if len(out) == k {
			break
		}
	}
	return out
}
