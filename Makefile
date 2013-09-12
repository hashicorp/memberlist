test: subnet
	go test ./...

subnet:
	./test/setup_subnet.sh

.PNONY: test
