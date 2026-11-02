.PHONY: test e2e test-native coverage race vet build integration package

export KIVRIX_TEST_ARTIFACTS ?= $(CURDIR)/build/test-artifacts

test:
	CGO_ENABLED=0 go test -count=1 ./...

e2e:
	CGO_ENABLED=0 go test -count=1 -run '^(TestE2E|TestAsync)' -v ./internal/app

test-native:
	CGO_ENABLED=1 KIVRIX_NATIVE_E2E=1 go test -tags=nativee2e -count=1 -timeout=45s -v ./tests/native

coverage:
	mkdir -p build
	CGO_ENABLED=0 go test -count=1 -coverprofile=build/coverage.out ./...
	go tool cover -func=build/coverage.out

race:
	go test -race -count=1 ./...

vet:
	go vet ./...
	go tool mygo vet ./internal/app

build:
	CGO_ENABLED=0 go build ./...

integration:
	./scripts/integration.sh

package:
	./scripts/package.sh $(ARGS)
