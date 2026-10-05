default:
    @just --list

test:
    go test ./...

cover:
    go test -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out | tail -1

lint:
    go vet ./...

# Positional arguments keep quoted paths with spaces intact.
[positional-arguments]
serve *args:
    go run ./cmd/whatnow serve "$@"
