.PHONY: test check pin update

test: check

check:
	go run ./cmd/pindock check -v -C .test; test $$? -eq 1

pin:
	go run ./cmd/pindock run -v -C .test

update:
	go run ./cmd/pindock run --update -v -C .test
