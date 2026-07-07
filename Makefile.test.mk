.PHONY: test check run copy pin update

test: check run

check:
	go run ./cmd/pindock check -v -C .test; test $$? -eq 1

run: copy pin update remove
run-update: copy update remove
copy:
	mkdir -p test; cp -r .test/* test/
pin:
	go run ./cmd/pindock run -v -C test
update:
	go run ./cmd/pindock run --update -v -C test
remove:
	rm -rf test
