// leurisreport turns the publications and projects fetched by leurisfetch into
// a single, self-contained HTML report: research areas, a timeline, funding,
// publishing habits, people and a few curiosities found in the data.
//
//	leurisreport -p projects.jsonl -b publications.jsonl > report.html
//	leurisreport -o report.md
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	_ "time/tzdata" // dates are interpreted in Europe/Berlin, also where no tz database is installed

	flag "github.com/spf13/pflag"
)

var (
	projectsFile     = flag.StringP("projects", "p", "projects.jsonl", "projects, as written by leurisfetch -k projects")
	publicationsFile = flag.StringP("publications", "b", "publications.jsonl", "publications, as written by leurisfetch -k publications")
	output           = flag.StringP("output", "o", "", "write the report to this file instead of stdout")
	format           = flag.StringP("format", "f", "", "report format, html or markdown (md); defaults to markdown for an output file ending in .md, html otherwise")
	verbose          = flag.BoolP("verbose", "v", false, "log progress to stderr")
)

// berlin is the time zone of the portal; LEURIS stores local midnight as UTC,
// so a project starting on 1 January shows up as 31 December 23:00Z.
var berlin = mustLoadLocation("Europe/Berlin")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		log.Fatal(err)
	}
	return loc
}

type person struct {
	FisPersid string `json:"fisPersid"` // set for members of Leipzig University
	FullName  string `json:"fullName"`
	Orcid     string `json:"orcid"`
}

type unit struct {
	ID      string `json:"id"`
	Path    string `json:"path"` // ids from the root, e.g. 1-3-414-936
	TitleDe string `json:"titleDe"`
}

type identifier struct {
	Type struct {
		Key string `json:"key"`
	} `json:"type"`
	Value string `json:"value"`
}

type publication struct {
	ID          string       `json:"_id"`
	Created     string       `json:"_createDate"`
	Updated     string       `json:"_updateDate"`
	Type        string       `json:"typeEn"`
	Title       string       `json:"title"`
	Year        int          `json:"year"`
	Language    string       `json:"language"`
	Journal     string       `json:"journal"`
	Authors     []person     `json:"authors"`
	Unit        *unit        `json:"organisationalUnit"`
	MoreUnits   []unit       `json:"additionalOrganisationalUnits"`
	Identifiers []identifier `json:"identifiers"`
	InProject   bool         `json:"inProject"`
	Validated   bool         `json:"validated"`
	Link        string       `json:"link"`

	// Derived fields, filled in by prepare.
	created time.Time
	area    string   // id of the faculty or central institution
	areas   []string // all areas involved, including additional units
	lang    string   // normalized language
	terms   []string // distinct title unigrams and bigrams
	hasDOI  bool
}

type funding struct {
	Code   string `json:"fundingCode"`
	Agency struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"fundingAgency"`
}

type project struct {
	ID            string    `json:"_id"`
	Created       string    `json:"_createDate"`
	Updated       string    `json:"_updateDate"`
	TitleDe       string    `json:"titleDe"`
	TitleEn       string    `json:"titleEn"`
	DescriptionDe string    `json:"descriptionDe"`
	DescriptionEn string    `json:"descriptionEn"`
	StartDate     string    `json:"startDate"`
	EndDate       string    `json:"endDate"`
	IsJoint       bool      `json:"isJointProject"`
	Superordinate string    `json:"superordinateProject"`
	Website       string    `json:"externalProjectWebsite"`
	Funding       []funding `json:"funding"`
	Applicants    []person  `json:"applicants"`
	Collaborators []person  `json:"collaborators"`
	Managers      []struct {
		Person person `json:"person"`
	} `json:"managers"`
	Unit      *unit  `json:"organisationalUnit"`
	MoreUnits []unit `json:"additionalOrganisationalUnits"`
	SDG       []struct {
		ID  string `json:"_id"`
		Rep string `json:"_stringRep"`
	} `json:"sdg"`
	PublicationsCount int    `json:"publications_count"`
	Link              string `json:"link"`

	// Derived fields, filled in by prepare.
	created    time.Time
	start, end time.Time // zero if missing
	area       string
	funders    []string // cleaned agency names
}

// title prefers the English title, as the report is in English.
func (p *project) title() string {
	if t := strings.TrimSpace(p.TitleEn); t != "" {
		return t
	}
	if t := strings.TrimSpace(p.TitleDe); t != "" {
		return t
	}
	return "(untitled project " + p.ID + ")"
}

// people returns everyone named on a project, once.
func (p *project) people() []person {
	var (
		out  []person
		seen = make(map[string]bool)
	)
	add := func(ps ...person) {
		for _, q := range ps {
			key := q.FisPersid
			if key == "" {
				key = q.FullName
			}
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, q)
		}
	}
	add(p.Applicants...)
	add(p.Collaborators...)
	for _, m := range p.Managers {
		add(m.Person)
	}
	return out
}

func readJSONL[T any](path string) ([]*T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var (
		dec = json.NewDecoder(bufio.NewReaderSize(f, 1<<20))
		out []*T
	)
	for {
		v := new(T)
		if err := dec.Decode(v); err == io.EOF {
			return out, nil
		} else if err != nil {
			return nil, fmt.Errorf("%s: record %d: %w", path, len(out)+1, err)
		}
		out = append(out, v)
	}
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: leurisreport [flags] > report.html\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	render := renderHTML
	switch f := strings.ToLower(*format); {
	case f == "markdown" || f == "md":
		render = renderMarkdown
	case f == "" && slices.Contains([]string{".md", ".markdown"}, strings.ToLower(filepath.Ext(*output))):
		render = renderMarkdown
	case f != "" && f != "html":
		log.Fatalf("unknown format %q, want html or markdown", *format)
	}
	started := time.Now()
	pubs, err := readJSONL[publication](*publicationsFile)
	if err != nil {
		log.Fatal(err)
	}
	projs, err := readJSONL[project](*projectsFile)
	if err != nil {
		log.Fatal(err)
	}
	if *verbose {
		log.Printf("read %d publications and %d projects in %s", len(pubs), len(projs), time.Since(started).Round(time.Millisecond))
	}
	if len(pubs) == 0 || len(projs) == 0 {
		log.Fatal("need both publications and projects")
	}
	d := prepare(pubs, projs)
	pg := build(d)
	out := os.Stdout
	if *output != "" {
		if out, err = os.Create(*output); err != nil {
			log.Fatal(err)
		}
	}
	bw := bufio.NewWriter(out)
	if err := render(bw, pg); err != nil {
		log.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		log.Fatal(err)
	}
	if err := out.Close(); err != nil {
		log.Fatal(err)
	}
	if *verbose {
		log.Printf("done in %s", time.Since(started).Round(time.Millisecond))
	}
}
