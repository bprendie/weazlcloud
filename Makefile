.PHONY: mockup desk-assets test vet race lines js-check build run check compose smoke-recovery smoke-browser smoke-container

VERSION ?= dev
LDFLAGS := -s -w -X github.com/bprendie/weazlcloud/internal/buildinfo.Version=$(VERSION)

mockup:
	python3 -m http.server 3001 --bind 127.0.0.1 --directory mockup-ui

test: desk-assets
	go test ./...

vet: desk-assets
	go vet ./...

race: desk-assets
	go test -race ./...

lines: desk-assets
	bash scripts/check-go-lines.sh

js-check:
	node --check mockup-ui/app.js
	node --check mockup-ui/data.js
	node --check mockup-ui/engine.js
	node --check mockup-ui/grab.js
	node --check mockup-ui/views.js

desk-assets:
	bash scripts/generate-desk-ui.sh

build: desk-assets
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o weazlcloud ./cmd/weazlcloud

run: build
	./weazlcloud

check: desk-assets test vet race lines js-check

smoke-recovery: desk-assets
	bash scripts/recovery-smoke.sh

smoke-browser: desk-assets
	bash scripts/smoke-browser.sh

smoke-container:
	bash scripts/container-smoke.sh

compose:
	docker compose -f deploy/compose.yaml up --build -d
