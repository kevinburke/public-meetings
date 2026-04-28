VENV := venv
WHISPER := $(VENV)/bin/mlx_whisper
DENO := bin/deno
GENERATE_TLS_CERT := $(GOPATH)/bin/generate-tls-cert

.PHONY: setup
setup: $(WHISPER) $(DENO) tmp/public-meetings

$(VENV)/bin/python:
	python3 -m venv $(VENV)

$(WHISPER): requirements.txt $(VENV)/bin/python
	$(VENV)/bin/pip install --upgrade pip
	$(VENV)/bin/pip install --requirement requirements.txt

$(DENO):
	curl -fsSL https://deno.land/install.sh | DENO_INSTALL=. sh

tmp/public-meetings: $(wildcard *.go) go.mod go.sum
	mkdir -p tmp
	GO111MODULE=on go build -trimpath -o tmp/ .

$(GENERATE_TLS_CERT):
	go install github.com/kevinburke/generate-tls-cert@latest

certs/leaf.pem: | $(GENERATE_TLS_CERT)
	mkdir -p certs
	cd certs && $(GENERATE_TLS_CERT) --host=localhost,127.0.0.1

tmp/serve: $(wildcard cmd/serve/*.go) go.mod go.sum
	mkdir -p tmp
	go build -trimpath -o tmp/serve ./cmd/serve

.PHONY: test
test:
	go test -trimpath ./...

.PHONY: release
release: test
	go run github.com/kevinburke/bump_version@latest --tag-prefix=v $(version) internal/version/version.go

.PHONY: serve
serve: certs/leaf.pem tmp/serve
	./tmp/serve

.PHONY: reset-db
reset-db:
	rm -f data/meetings.json

.PHONY: clean-data
clean-data:
	rm -rf data

.PHONY: clean
clean:
	rm -rf tmp

.PHONY: clean-all
clean-all: clean
	rm -rf $(VENV) bin
