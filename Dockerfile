# Builds whatnow as a small image. By default it runs `whatnow serve`, the same as on your own
# machine, with its database in the /data volume:
#   docker build -t whatnow .
#   docker run -p 127.0.0.1:7777:7777 -v whatnow-data:/data whatnow
# Then open http://localhost:7777. The hosted server (`whatnow host`) runs from the same image:
# see docs/hosting.md.

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
EXPOSE 7777
# Listens on every interface so Docker can reach it. Keep the published port on 127.0.0.1: the
# server rejects requests whose Host isn't this machine, but nothing else guards it.
ENTRYPOINT ["whatnow"]
CMD ["serve", "-addr", "0.0.0.0:7777", "-db", "/data/whatnow.db"]
