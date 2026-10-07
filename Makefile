SHELL := /bin/bash
TARGETS := leurisfetch

.PHONY: all
all: $(TARGETS)

leurisfetch: main.go go.mod go.sum
	go build -o $@ .

# Data files only need the binary to exist (order-only), so a rebuild does not
# trigger a refetch; write to a tmp file, so a failed run leaves no partial file.
publications.jsonl: | leurisfetch
	./leurisfetch -v -k publications > $@.tmp && mv $@.tmp $@

projects.jsonl: | leurisfetch
	./leurisfetch -v -k projects > $@.tmp && mv $@.tmp $@

.PHONY: data
data: publications.jsonl projects.jsonl

.PHONY: clean
clean:
	rm -f $(TARGETS) *.jsonl.tmp
