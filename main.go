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
        }
      }
    }
  }
}`

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
        }
      }
    }
  }
}`

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
		client = &http.Client{Timeout: *timeout}
		bw     = bufio.NewWriter(os.Stdout)
		buf    bytes.Buffer
	)
	for i := 1; ; i++ {
		p, err := fetchPageRetry(client, k, map[string]any{
			"id":        *unit,
			"pageSize":  *pageSize,
			"page":      i,
			"sortOrder": "DESC",
			"sortField": k.sortField,
		})
		if err != nil {
			bw.Flush()
			log.Fatal(err)
		}
		for _, e := range p.Entries {
			buf.Reset()
			if err := json.Compact(&buf, e); err != nil {
				log.Fatal(err)
			}
			buf.WriteByte('\n')
			if _, err := bw.Write(buf.Bytes()); err != nil {
				log.Fatal(err)
			}
		}
		if *verbose {
			log.Printf("page %d/%d, %d entries total", i, p.Meta.TotalPages, p.Meta.TotalEntries)
		}
		if len(p.Entries) == 0 || i >= p.Meta.TotalPages || (*maxPages > 0 && i >= *maxPages) {
			break
		}
		time.Sleep(*sleep)
	}
	if err := bw.Flush(); err != nil {
		log.Fatal(err)
	}
}
