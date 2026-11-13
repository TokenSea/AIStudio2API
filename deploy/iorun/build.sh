#!/bin/sh
set -eu
# Same release flags as Dockerfile.iorun; an existing isolated builder image
# may supply its compiler cache. Source and output must be separate mounts.
export GOMAXPROCS=2 GOMEMLIMIT=768MiB GOFLAGS=-p=1 CGO_ENABLED=1 GOOS=linux GOARCH=amd64
export CC='/opt/zig/zig cc -target x86_64-linux-musl'
export CXX='/opt/zig/zig c++ -target x86_64-linux-musl'
export CGO_CPPFLAGS='-I/opt/spidermonkey/include -I/opt/libwebp/include'
export CGO_LDFLAGS='-L/opt/spidermonkey/lib -L/opt/libwebp/lib'
go build -trimpath -tags netgo,osusergo -ldflags="-s -w -linkmode external -extldflags '-static -Wl,--strip-all'" -o /out/aistudio2api ./cmd/aistudio2api
