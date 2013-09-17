test: subnet
	go test ./...

subnet:
	./test/setup_subnet.sh

cov:
	gocov test github.com/hashicorp/memberlist | gocov-html > /tmp/coverage.html
	open /tmp/coverage.html

.PNONY: test cov
