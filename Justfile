default:
    @just --list

test:
    go test ./...
    cd web && npm test

cover:
    go test -coverpkg=./... -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out | tail -1

lint:
    go vet ./...
    cd web && npm run check

# Builds the web client into the directory the Go binary embeds.
web:
    cd web && npm ci && npm run build
    find internal/web/dist -mindepth 1 ! -name .gitkeep -delete
    cp -R web/build/. internal/web/dist/

build: web
    go build -o whatnow ./cmd/whatnow

# Positional arguments keep quoted paths with spaces intact.
[positional-arguments]
serve *args:
    go run ./cmd/whatnow serve "$@"

# Runs the API and the Vite dev server, which proxies /api to it. Open http://localhost:5173.
[positional-arguments]
dev *args:
    #!/usr/bin/env bash
    trap 'kill 0' EXIT
    go run ./cmd/whatnow serve "$@" &
    cd web && npm run dev

# Runs the Playwright suite against a server built with the fake model, so no model is ever called.
e2e: web
    go build -tags e2e -o whatnow-e2e ./cmd/whatnow
    cd web && npx playwright install chromium && npx playwright test
