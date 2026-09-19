# ghost: build, lint and test both modules. Requires Go 1.25+.
MODULES := ghost-go ghost-server

# each runs a command in every module: $(call each,go vet ./...)
each = for m in $(MODULES); do echo "== $$m"; (cd $$m && $(1)) || exit 1; done

.PHONY: all build vet test test-race lint fmt docker

all: vet test

build:
	@$(call each,go build ./...)
vet:
	@$(call each,go vet ./...)
test:
	@$(call each,go test ./...)
test-race:
	@$(call each,go test -race ./...)
lint:
	@$(call each,golangci-lint run ./...)
fmt:
	@$(call each,golangci-lint fmt ./...)

docker:
	docker build -f ghost-server/Dockerfile -t ghost-server:dev .
