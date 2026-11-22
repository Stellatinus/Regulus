.PHONY: dashboard build lint test test-postgres test-one-connection smoke e2e fuzz proto proto-check proto-breaking clean

# Build the dashboard into internal/webui/dist so it is embedded in the binary.
dashboard:
	cd dashboard && npm ci --ignore-scripts && npm run build

build: dashboard
	go build -ldflags "-X main.version=$$(git describe --tags --always --dirty)" -o bin/regulus ./cmd/regulus

lint:
	golangci-lint run
	cd dashboard && npm ci && npm run lint

test:
	sh scripts/test-install.sh
	sh scripts/test-next-version.sh
	sh scripts/test-update-recover.sh
	sh scripts/test-package-plugin.sh
	go vet ./...
	go test -race ./...
	cd dashboard && npm ci && npm run typecheck && npm test

# Run the database-dependent tests on PostgreSQL as well. Needs a database you
# can write to, for example:
#   docker run --rm -d -p 5432:5432 -e POSTGRES_USER=regulus -e POSTGRES_PASSWORD=regulus -e POSTGRES_DB=regulus_test postgres:16
#   make test-postgres REGULUS_TEST_DATABASE_URL='postgres://regulus:regulus@localhost:5432/regulus_test?sslmode=disable'
# Each test gets its own schema, dropped when it ends.
test-postgres:
	@test -n "$(REGULUS_TEST_DATABASE_URL)" || { echo 'set REGULUS_TEST_DATABASE_URL (see the comment above this target)'; exit 2; }
	REGULUS_TEST_DATABASE_URL='$(REGULUS_TEST_DATABASE_URL)' go test -race ./internal/storage/... ./internal/auth/... ./internal/fleet/... ./internal/hub/... ./internal/update/... ./cmd/...

# Run the hub tests with one SQLite connection for reads and writes (no read
# pool): a query made while the code holds a transaction or a result set of
# its own then hangs instead of passing unnoticed (#219).
test-one-connection:
	REGULUS_TEST_ONE_CONNECTION=1 go test -race -timeout 10m ./internal/hub/...

# Start the built binary and check it end to end (run make build first).
smoke:
	scripts/smoke.sh bin/regulus

# End-to-end test of a hub and a remote agent, each the real binary (built
# with -race by the test) in its own process, over the mTLS agent listener.
# It needs no dashboard build. See internal/e2e and docs/development.md. One
# test downloads the previous published release's agent (see
# TestAgentOfAnOlderRelease); it skips, saying why, when it cannot.
e2e:
	REGULUS_E2E=1 go test -race -count=1 -timeout 10m -v ./internal/e2e/

# Run every fuzz target for FUZZTIME each (the nightly Fuzz workflow uses 30s).
# A plain `go test` already runs their seed corpora.
FUZZTIME ?= 30s
FUZZ_TARGETS := ./internal/agent:FuzzParseCPUTimes ./internal/agent:FuzzParseMemInfo \
	./internal/agent:FuzzParseMounts ./internal/agent:FuzzParseNetDev \
	./internal/agent:FuzzUnescapeMount ./internal/hub:FuzzNetworkRate
fuzz:
	@set -e; for t in $(FUZZ_TARGETS); do \
		echo "fuzz $${t#*:}"; \
		go test $${t%%:*} -run '^$$' -fuzz "^$${t#*:}$$" -fuzztime $(FUZZTIME); \
	done

# Regenerate internal/gen from proto/ (generated code is committed, so a normal
# build does not need these tools). Tool versions are pinned here.
TOOLS := $(CURDIR)/bin/tools
proto:
	GOBIN=$(TOOLS) go install github.com/bufbuild/buf/cmd/buf@v1.75.0
	GOBIN=$(TOOLS) go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
	GOBIN=$(TOOLS) go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
	PATH=$(TOOLS):$$PATH buf lint proto
	PATH=$(TOOLS):$$PATH buf generate

# Fails if the committed generated code is out of date.
proto-check: proto
	@test -z "$$(git status --porcelain -- internal/gen)" || { echo 'internal/gen is out of date: run make proto and commit'; git status --short -- internal/gen; exit 1; }

# Fails if proto/ changed in a way an older hub or agent cannot read (a field
# renumbered, retyped or removed; see docs/versioning.md). Compares with the
# main branch; CI passes the repository address, since its checkout has no
# other branch. Example against a release: make proto-breaking PROTO_AGAINST='../.git#tag=v0.2.0,subdir=proto' (the path is relative to proto/)
PROTO_AGAINST ?= ../.git\#branch=main,subdir=proto
proto-breaking:
	GOBIN=$(TOOLS) go install github.com/bufbuild/buf/cmd/buf@v1.75.0
	cd proto && $(TOOLS)/buf breaking --against '$(PROTO_AGAINST)'

clean:
	rm -rf bin internal/webui/dist/* dashboard/node_modules
	touch internal/webui/dist/.gitkeep
