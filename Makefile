SHELL := /bin/bash
TARGETS := leurisfetch leurisreport

.PHONY: all
all: $(TARGETS)

leurisfetch: main.go go.mod go.sum
	go build -o $@ .

leurisreport: $(wildcard cmd/leurisreport/*.go) go.mod go.sum
	go build -o $@ ./cmd/leurisreport

# Data files only need the binary to exist (order-only), so a rebuild does not
# trigger a refetch; a failed run leaves a tmp file, which the next run resumes.
publications.jsonl: | leurisfetch
	./leurisfetch -v -k publications -R $@.tmp && mv $@.tmp $@

projects.jsonl: | leurisfetch
	./leurisfetch -v -k projects -R $@.tmp && mv $@.tmp $@

.PHONY: data
data: publications.jsonl projects.jsonl

# Reports, as HTML with charts or as Markdown with tables.
report.html report.md: publications.jsonl projects.jsonl leurisreport
	./leurisreport -o $@

.PHONY: report
report: report.html report.md

.PHONY: clean
clean:
	rm -f $(TARGETS) *.jsonl.tmp report.html report.md
