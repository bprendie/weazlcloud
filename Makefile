.PHONY: mockup desk-assets test vet race lines js-check build run check compose smoke-recovery smoke-browser smoke-container smoke-sharedstore smoke-photos smoke-mobile

VERSION ?= dev
PHOTOS_PYTHON ?= python3
MOBILE_PYTHON ?= python3
LDFLAGS := -s -w -X github.com/bprendie/weazlcloud/internal/buildinfo.Version=$(VERSION)
export PATH := $(CURDIR)/.build:$(PATH)

.PHONY: metadata-reader
metadata-reader:
	mkdir -p .build
	cd native/restic-reader && CGO_ENABLED=0 go build -trimpath -o ../../.build/weazl-restic-reader .
	cd native/restic-reader && go vet ./... && go test -race ./...

mockup:
	python3 -m http.server 3001 --bind 127.0.0.1 --directory mockup-ui

test: desk-assets metadata-reader
	go test ./...

vet: desk-assets
	go vet ./...

race: desk-assets metadata-reader
	go test -race ./...

lines: desk-assets
	bash scripts/check-go-lines.sh

js-check:
	node --input-type=module --check < mockup-ui/app.js
	node --input-type=module --check < mockup-ui/data.js
	node --input-type=module --check < mockup-ui/engine.js
	node --input-type=module --check < mockup-ui/grab.js
	node --input-type=module --check < mockup-ui/views.js
	node --input-type=module --check < mockup-ui/photo-layout.js
	node --input-type=module --check < mockup-ui/photo-timeline.js
	node --input-type=module --check < mockup-ui/photo-cache.js
	node --input-type=module --check < mockup-ui/photo-images.js
	node --input-type=module --check < mockup-ui/photo-dom.js
	node --input-type=module --check < mockup-ui/photo-zoom.js
	node --input-type=module --check < mockup-ui/photo-gestures.js
	node --input-type=module --check < mockup-ui/photo-placeholder.js
	node scripts/photo-cache.test.mjs
	node --input-type=module --check < mockup-ui/mode-memory.js
	node scripts/photo-layout.test.mjs
	node --input-type=module --check < mockup-ui/photo-scroll.js
	node --input-type=module --check < mockup-ui/photo-pages.js
	node scripts/photo-navigation.test.mjs
	node scripts/mode-memory.test.mjs

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
	WEAZLCLOUD_BROWSER_PORT=28772 bash scripts/smoke-browser.sh
	WEAZLCLOUD_BROWSER_PORT=28872 WEAZLCLOUD_SMOKE_STORAGE_BACKEND=shared-experimental bash scripts/smoke-browser.sh

smoke-worker:
	WEAZLCLOUD_IMAGE=$(WEAZLCLOUD_IMAGE) bash scripts/smoke-photo-worker.sh

smoke-container:
	docker build -f deploy/Dockerfile -t weazlcloud:smoke .
	WEAZLCLOUD_IMAGE=weazlcloud:smoke bash scripts/smoke-photo-worker.sh
	WEAZLCLOUD_IMAGE=weazlcloud:smoke bash scripts/container-smoke.sh
	WEAZLCLOUD_IMAGE=weazlcloud:smoke WEAZLCLOUD_SMOKE_STORAGE_BACKEND=shared-experimental bash scripts/container-smoke.sh

smoke-sharedstore:
	bash scripts/sharedstore-smoke.sh

# Build weazlcloud:smoke first; requires Playwright and a Chromium installation.
smoke-photos:
	WEAZLCLOUD_IMAGE=weazlcloud:smoke WEAZLCLOUD_SMOKE_CPUS=2 WEAZLCLOUD_SMOKE_MEMORY=4g $(PHOTOS_PYTHON) -u scripts/smoke-photo-albums.py
	WEAZLCLOUD_IMAGE=weazlcloud:smoke WEAZLCLOUD_SMOKE_CPUS=2 WEAZLCLOUD_SMOKE_MEMORY=4g WEAZLCLOUD_SMOKE_STORAGE_BACKEND=shared-experimental $(PHOTOS_PYTHON) -u scripts/smoke-photo-albums.py

# Build a local weazlcloud:smoke image first; standard-library Python, both backends.
# WEAZLCLOUD_IMAGE may select another already-built local release candidate.
smoke-mobile:
	WEAZLCLOUD_SMOKE_CPUS=2 WEAZLCLOUD_SMOKE_MEMORY=4g $(MOBILE_PYTHON) -u scripts/smoke-mobile-server.py

compose:
	docker compose -f deploy/compose.yaml up --build -d
