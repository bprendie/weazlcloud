.PHONY: mockup test race lines build run check

VERSION ?= dev
LDFLAGS := -s -w -X github.com/bprendie/weazlcloud/internal/buildinfo.Version=$(VERSION)

mockup:
	python3 -m http.server 3001 --bind 127.0.0.1 --directory mockup-ui

test:
	go test ./...

race:
	go test -race ./...

lines:
	bash scripts/check-go-lines.sh

desk-assets:
	rm -rf internal/desk/ui
	mkdir -p internal/desk/ui
	cp -a mockup-ui/. internal/desk/ui/

build: desk-assets
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o weazlcloud ./cmd/weazlcloud

run: build
	./weazlcloud

check: test race lines

compose:
	docker compose -f deploy/compose.yaml up --build -d
