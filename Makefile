.PHONY: build test vet fmt run bundle goldens perf docker

BIN := humantxt

build:
	go build -trimpath -o $(BIN) ./cmd/humantxt

test:
	go test ./...

fmt:
	gofmt -l cmd internal

vet: fmt
	go vet ./...

run: build
	./$(BIN)

# Rebuild the bundle the binary embeds. Uses the local node when present, docker otherwise.
bundle:
	@if command -v npm > /dev/null; then cd js && npm install && npm run build; \
	else docker run --rm -v "$(CURDIR):/src" -w /src/js node:24-alpine sh -c "npm install && npm run build"; fi

# Rebuild the parser output the vm tests are compared against; needs node.
# both columns of the cost table in the README
perf:
	node js/perf.mjs
	go test -run '^$$' -bench Extraction -benchtime 3x ./internal/vm/

goldens:
	cd js && npm install && node golden.mjs

docker:
	docker build -t humantxt .
