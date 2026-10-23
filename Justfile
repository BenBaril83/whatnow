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

# Puts whatnow on your PATH, in GOBIN.
install: web
    go install ./cmd/whatnow

# Builds the stripped Linux binary that `whatnow host` runs from, into dist/.
build-linux: web
    #!/usr/bin/env bash
    set -euo pipefail
    v=$(git describe --tags --always)
    mkdir -p dist
    GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -X main.Version=${v#v}" -o dist/whatnow-linux-amd64 ./cmd/whatnow

# Tests, then ships a Linux build to the hosted server set up by docs/hosting.md, restarts it, and checks it answers.
deploy server="root@whatnow.day": test build-linux
    #!/usr/bin/env bash
    set -euo pipefail
    host={{server}}
    host=${host#*@}
    scp -q dist/whatnow-linux-amd64 {{server}}:/usr/local/bin/whatnow.new
    ssh {{server}} 'mv /usr/local/bin/whatnow.new /usr/local/bin/whatnow && systemctl restart whatnow && sleep 2 && systemctl is-active --quiet whatnow || { journalctl -u whatnow -n 20 --no-pager; exit 1; }'
    curl -fsS https://$host/api/v1/session >/dev/null
    echo "deployed $(git describe --tags --always) to $host"

# Prints how many accounts the hosted server has, against its -max-users cap.
users server="root@whatnow.day":
    ssh {{server}} "sqlite3 -readonly /mnt/whatnow/accounts.db 'select count(*) from users;'"

# Builds stripped Mac binaries, publishes them as a GitHub release, and points the Homebrew formula in jameynakama/homebrew-tap at them: `just release v0.1.0`. The tag lands on main as GitHub has it, so push first.
release version: web
    #!/usr/bin/env bash
    set -euo pipefail
    v={{version}}
    rm -rf dist && mkdir dist
    for arch in arm64 amd64; do
        GOOS=darwin GOARCH=$arch go build -trimpath -ldflags="-s -w -X main.Version=${v#v}" -o dist/whatnow-darwin-$arch ./cmd/whatnow
    done
    gh release create $v dist/whatnow-darwin-* --title $v --generate-notes
    tap=$(mktemp -d)
    trap 'rm -rf "$tap"' EXIT
    gh repo clone jameynakama/homebrew-tap "$tap" -- --depth 1 --quiet
    mkdir -p "$tap/Formula"
    sed -e "s/@VERSION@/${v#v}/g" \
        -e "s/@SHA_ARM64@/$(shasum -a 256 dist/whatnow-darwin-arm64 | cut -d' ' -f1)/" \
        -e "s/@SHA_AMD64@/$(shasum -a 256 dist/whatnow-darwin-amd64 | cut -d' ' -f1)/" \
        packaging/whatnow.rb > "$tap/Formula/whatnow.rb"
    git -C "$tap" add Formula/whatnow.rb
    git -C "$tap" commit --quiet -m "whatnow $v"
    git -C "$tap" push --quiet

# Positional arguments keep quoted paths with spaces intact.
[positional-arguments]
serve *args:
    go run ./cmd/whatnow serve "$@"

# The terminal companion. Start and wrap up the day in the web UI.
[positional-arguments]
tui *args:
    go run ./cmd/whatnow tui "$@"

# Runs the API and the Vite dev server, which proxies /api to it. Open http://localhost:5173.
[positional-arguments]
dev *args:
    #!/usr/bin/env bash
    trap 'kill 0' EXIT
    go run ./cmd/whatnow serve "$@" &
    cd web && npm run dev

# Runs the Playwright suite against a server built with the fake model, so no model is ever called.
e2e: web
    cd web && npx playwright install chromium && npx playwright test

# A demo run on its own database and port, on a clock `just clock` moves. Delete .demo to start over.
[positional-arguments]
demo *args: web
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p .demo
    [[ -f .demo/clock ]] || echo "$(date +%F) 08:40" > .demo/clock
    WHATNOW_DEMO_CLOCK="$PWD/.demo/clock" go run ./cmd/whatnow -addr 127.0.0.1:7778 -db .demo/whatnow.db "$@"

# Moves the demo clock: `just clock 13:45` on the same day, or `just clock 2026-10-09 08:40`.
[positional-arguments]
clock *when:
    #!/usr/bin/env bash
    set -euo pipefail
    when="$*"
    [[ $when == *-* ]] || when="$(cut -d' ' -f1 .demo/clock) $when"
    date -j -f "%Y-%m-%d %H:%M" "$when" >/dev/null
    echo "$when" > .demo/clock
