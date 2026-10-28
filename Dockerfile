# Builds the hosted server (`whatnow host`) as a small image. See docs/hosting.md for what it needs.
#   docker build -t whatnow .
#   docker run -p 8080:8080 -v whatnow-data:/data --env-file whatnow.env whatnow -public-url https://whatnow.example.com

FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The Go binary embeds the web client from internal/web/dist, as `just web` does.
RUN find internal/web/dist -mindepth 1 ! -name .gitkeep -delete
COPY --from=web /src/web/build/ internal/web/dist/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.Version=${VERSION}" -o /whatnow ./cmd/whatnow

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H -h /data whatnow \
    && mkdir /data && chown whatnow:whatnow /data
COPY --from=build /whatnow /usr/local/bin/whatnow
USER whatnow
VOLUME /data
EXPOSE 8080
# TLS is terminated by a proxy in front (Caddy, a load balancer), so -public-url must be its https URL.
# WHATNOW_GOOGLE_CLIENT_ID, WHATNOW_GOOGLE_CLIENT_SECRET and WHATNOW_SEAL_KEY come from the environment.
ENTRYPOINT ["whatnow", "host", "-addr", "0.0.0.0:8080", "-data", "/data"]
