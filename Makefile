.PHONY: all build test clean build-ts build-go test-ts test-go clean-ts clean-go publish-go publish-go-markdown tags-go tags-go-markdown tidy-go reset

all: build test

build: build-ts build-go

test: test-ts test-go

clean: clean-ts clean-go

# TypeScript
build-ts:
	npm run build

test-ts:
	npm test

clean-ts:
	rm -rf dist dist-test

# Go
build-go:
	cd go && go build ./...

test-go:
	cd go && go test ./...

clean-go:
	cd go && go clean -cache

# Publish Go csv module: make publish-go V=0.1.7
publish-go: test-go
	@test -n "$(V)" || (echo "Usage: make publish-go V=x.y.z" && exit 1)
	sed -i '' 's/^const Version = ".*"/const Version = "$(V)"/' go/csv.go
	git add go/csv.go
	git commit -m "go: v$(V)"
	git tag go/v$(V)
	git push origin main go/v$(V)
	if command -v gh >/dev/null 2>&1; then gh release create go/v$(V) --title "go/v$(V)" --notes "Go module release v$(V)"; fi

# Publish Go markdown sub-module: make publish-go-markdown V=0.2.0
publish-go-markdown: test-go
	@test -n "$(V)" || (echo "Usage: make publish-go-markdown V=x.y.z" && exit 1)
	sed -i '' 's/^const Version = ".*"/const Version = "$(V)"/' go/markdown/markdown.go
	git add go/markdown/markdown.go
	git commit -m "go/markdown: v$(V)"
	git tag go/markdown/v$(V)
	git push origin main go/markdown/v$(V)
	if command -v gh >/dev/null 2>&1; then gh release create go/markdown/v$(V) --title "go/markdown/v$(V)" --notes "Go markdown module release v$(V)"; fi

tidy-go:
	cd go && go mod tidy

tags-go:
	git tag -l 'go/v*' --sort=-version:refname

tags-go-markdown:
	git tag -l 'go/markdown/v*' --sort=-version:refname

reset:
	npm run reset
	cd go && go clean -cache
	cd go && go build ./...
	cd go && go test -v ./...
