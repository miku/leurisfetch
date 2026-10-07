// leurisfetch fetches publications or research projects of an organisational
// unit from the LEURIS GraphQL API (https://leuris.uni-leipzig.de) and writes
// each entry as a single JSON line to stdout.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	flag "github.com/spf13/pflag"
)

const apiURL = "https://leuris.uni-leipzig.de/anchorwheel/api"

// Queries are based on the ones the LEURIS portal sends, with additional
// structured fields; see notes/schema.json for everything available.
const fragments = `
fragment person on Person { fisPersid fullName givenName surname orcid }
fragment unit on OrganisationalUnit { id titleDe path }`

const publicationQuery = `
query fetchPublicationQuery(
  $id: ID!,
  $pageSize: Int!,
  $page: Int!,
  $sortOrder: SortOrder,
  $sortField: PublicationSortKeys,
  $filterYear: Int,
  $filterFreetext: String,
  $filterPerson: String,
  $filterCategory: String,
  $filterInProject: Boolean,
  $filterFundedByCategory: FundingAgencyCategory,
) {
  organisationalUnit(id: $id) {
    publikation:publications(
      pageSize: $pageSize, page: $page, sortOrder: $sortOrder, orderBy:$sortField,
      filter: {
        year: $filterYear
        freeText: $filterFreetext
        person: $filterPerson
        category: $filterCategory
        inProject: $filterInProject
        fundedByCategory: $filterFundedByCategory
      }
    ) {
      meta { pageNumber pageSize totalEntries totalPages }
      entries {
        ... on Publication {
          _id:id
          _entityName
          publikationsTyp:typeDe
          html { de en }
          typeEn title year language
          journal bookTitle seriesTitle volume issue pageStart pageEnd
          publisher place issuingOrganisation
          identifiers { type { key } value }
          authors { ...person }
          editors { ...person }
          organisationalUnit { ...unit }
          additionalOrganisationalUnits { ...unit }
          fundingAgencies { id name }
          project { id titleDe titleEn }
          inProject validated link _createDate _updateDate
        }
      }
    }
  }
}` + fragments

const projectQuery = `
query fetchResearchProjectQuery(
  $id: ID!,
  $pageSize:Int!,
  $page:Int!,
  $sortOrder:SortOrder,
  $sortField:ProjectSortKeys,
  $filterYear:Int,
  $filterFreetext:String,
  $filterFunding:String,
  $filterSdgCategory:SdgCategory,
  $filterSdgIds:[Int],
  $filterPerson:String,
  $filterStatus:String
) {
  organisationalUnit(id: $id) {
    forschungsprojekt:projects(
      pageSize: $pageSize, page: $page, sortOrder: $sortOrder, orderBy:$sortField,
      filter: {
        year: $filterYear
        freeText: $filterFreetext
        funding: $filterFunding
        person: $filterPerson
        sdgCategory: $filterSdgCategory
        sdgIds: $filterSdgIds
        status: $filterStatus
      }
    ) {
      meta { totalPages totalEntries pageNumber pageSize }
      entries {
        ... on Project {
          _id:id
          _entityName
          primaryPublicIdent
          sdg { _id:id _stringRep }
          html { de en }
          publications_count
          titleDe titleEn descriptionDe descriptionEn startDate endDate
          isJointProject superordinateProject externalProjectWebsite
          funding { fundingCode fundingAgency { id name } }
          applicants { ...person }
          collaborators { ...person }
          managers { startDate endDate person { ...person } }
          organisationalUnit { ...unit }
          additionalOrganisationalUnits { ...unit }
          spokespersonOu { id name city }
          publications { id }
          link _createDate _updateDate
        }
      }
    }
  }
}` + fragments

// kind describes one paginated collection we can fetch.
type kind struct {
	op        string // GraphQL operation name
	query     string
	field     string // alias of the paginated field in the response
	sortField string
}

var kinds = map[string]kind{
	"publications": {"fetchPublicationQuery", publicationQuery, "publikation", "YEAR"},
	"projects":     {"fetchResearchProjectQuery", projectQuery, "forschungsprojekt", "START_DATE"},
}

type page struct {
	Meta struct {
		PageNumber   int `json:"pageNumber"`
		PageSize     int `json:"pageSize"`
		TotalEntries int `json:"totalEntries"`
		TotalPages   int `json:"totalPages"`
	} `json:"meta"`
	Entries []json.RawMessage `json:"entries"`
}

type response struct {
	Data struct {
		OrganisationalUnit map[string]page `json:"organisationalUnit"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

var (
	kindName  = flag.StringP("kind", "k", "publications", "what to fetch: publications or projects")
	unit      = flag.IntP("unit", "u", 1, "organisational unit id")
	pageSize  = flag.IntP("page-size", "s", 100, "entries per request")
	maxPages  = flag.IntP("max-pages", "m", 0, "stop after this many pages (0 = all)")
	cookie    = flag.StringP("cookie", "c", os.Getenv("LEURIS_COOKIE"), "cookie header value, defaults to $LEURIS_COOKIE")
	sleep     = flag.DurationP("sleep", "w", 500*time.Millisecond, "pause between requests")
	timeout   = flag.DurationP("timeout", "t", 60*time.Second, "http timeout per request")
	retries   = flag.IntP("retries", "r", 3, "attempts per page")
	resume    = flag.StringP("resume", "R", "", "append to this file instead of stdout, continuing an interrupted fetch")
	verbose   = flag.BoolP("verbose", "v", false, "log progress to stderr")
	userAgent = "leurisfetch/0.1 (+https://github.com/miku/leurisfetch)"
)

func fetchPage(client *http.Client, k kind, vars map[string]any) (*page, error) {
	body, err := json.Marshal(map[string]any{
		"operationName": k.op,
		"query":         k.query,
		"variables":     vars,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json;charset=utf-8")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if *cookie != "" {
		req.Header.Set("Cookie", *cookie)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, b)
	}
	var r response
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if len(r.Errors) > 0 {
		return nil, fmt.Errorf("graphql: %s", r.Errors[0].Message)
	}
	p, ok := r.Data.OrganisationalUnit[k.field]
	if !ok {
		return nil, fmt.Errorf("no %q in response, unknown unit?", k.field)
	}
	return &p, nil
}

func fetchPageRetry(client *http.Client, k kind, vars map[string]any) (p *page, err error) {
	for i := 1; i <= max(*retries, 1); i++ {
		if p, err = fetchPage(client, k, vars); err == nil {
			return p, nil
		}
		log.Printf("page %v, attempt %d: %v", vars["page"], i, err)
		time.Sleep(time.Duration(i) * 2 * time.Second)
	}
	return nil, err
}

// entryID returns the id of a single JSON entry.
func entryID(b []byte) (string, error) {
	var v struct {
		ID string `json:"_id"`
	}
	err := json.Unmarshal(b, &v)
	return v.ID, err
}

// openResume opens path for appending, creating it if necessary. It drops a
// trailing incomplete line, as left by an interrupted run, and returns the ids
// of all entries already in the file.
func openResume(path string) (*os.File, map[string]bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return nil, nil, err
	}
	var (
		seen   = make(map[string]bool)
		br     = bufio.NewReader(f)
		offset int64
	)
	for {
		b, err := br.ReadBytes('\n')
		if err == io.EOF {
			break // b holds an incomplete line, if any
		}
		if err != nil {
			f.Close()
			return nil, nil, err
		}
		id, err := entryID(b)
		if err != nil {
			f.Close()
			return nil, nil, fmt.Errorf("%s: line at offset %d: %w", path, offset, err)
		}
		if id != "" {
			seen[id] = true
		}
		offset += int64(len(b))
	}
	if err := f.Truncate(offset); err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, seen, nil
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: leurisfetch [flags] > out.jsonl\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	k, ok := kinds[*kindName]
	if !ok {
		log.Fatalf("unknown kind %q, want publications or projects", *kindName)
	}
	var (
		out   = os.Stdout
		seen  = make(map[string]bool) // also drops duplicates from shifting pages
		start = 1
	)
	if *resume != "" {
		f, ids, err := openResume(*resume)
		if err != nil {
			log.Fatal(err)
		}
		out, seen = f, ids
		// Entries may have shifted since the last run, so start a page early;
		// entries already in the file get skipped anyway.
		start = max(1, len(seen) / *pageSize)
		if *verbose {
			log.Printf("resuming %s with %d entries at page %d", *resume, len(seen), start)
		}
	}
	var (
		client = &http.Client{Timeout: *timeout}
		bw     = bufio.NewWriter(out)
		buf    bytes.Buffer
	)
	for i := start; ; i++ {
		p, err := fetchPageRetry(client, k, map[string]any{
			"id":        *unit,
			"pageSize":  *pageSize,
			"page":      i,
			"sortOrder": "DESC",
			"sortField": k.sortField,
		})
		if err != nil {
			log.Fatal(err)
		}
		var added int
		for _, e := range p.Entries {
			id, err := entryID(e)
			if err != nil {
				log.Fatal(err)
			}
			if id != "" && seen[id] {
				continue
			}
			seen[id] = true
			buf.Reset()
			if err := json.Compact(&buf, e); err != nil {
				log.Fatal(err)
			}
			buf.WriteByte('\n')
			if _, err := bw.Write(buf.Bytes()); err != nil {
				log.Fatal(err)
			}
			added++
		}
		// Flush per page, so an interrupted run loses at most the current page.
		if err := bw.Flush(); err != nil {
			log.Fatal(err)
		}
		if *verbose {
			log.Printf("page %d/%d, %d new, %d entries total", i, p.Meta.TotalPages, added, p.Meta.TotalEntries)
		}
		if len(p.Entries) == 0 || i >= p.Meta.TotalPages || (*maxPages > 0 && i-start+1 >= *maxPages) {
			break
		}
		time.Sleep(*sleep)
	}
	if err := out.Close(); err != nil {
		log.Fatal(err)
	}
}
