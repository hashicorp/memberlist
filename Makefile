SHELL := bash
DEPS := $(shell go list -f '{{range .Imports}}{{.}} {{end}}' ./...)
GOFILES ?= $(shell go list $(GOMODULES) | grep -v /vendor/)

test: vet subnet
	go test ./...

integ: subnet
	INTEG_TESTS=yes go test ./...

subnet:
	./test/setup_subnet.sh

cov:
	gocov test github.com/hashicorp/memberlist | gocov-html > /tmp/coverage.html
	open /tmp/coverage.html

format:
	@echo "--> Running go fmt"
	@go fmt $(GOFILES)

vet:
	@echo "--> Running go vet"
	@go vet -tags '$(GOTAGS)' $(GOFILES); if [ $$? -eq 1 ]; then \
		echo ""; \
		echo "Vet found suspicious constructs. Please check the reported constructs"; \
		echo "and fix them if necessary before submitting the code for review."; \
		exit 1; \
	fi

deps:
	go get -t -d -v ./...
	echo $(DEPS) | xargs -n1 go get -d

.PHONY: test cov integ
