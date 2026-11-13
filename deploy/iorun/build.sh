#!/bin/sh
set -eu
# Native GNU release for ciorun03, matching the tested Debian toolchain.
export GOMAXPROCS=2 GOMEMLIMIT=768MiB GOFLAGS=-p=1 CGO_ENABLED=1 GOOS=linux GOARCH=amd64
export CC=gcc CXX=g++
export CGO_CPPFLAGS='-I/opt/spidermonkey-gnu/include -I/opt/libwebp-gnu/include'
export CGO_LDFLAGS='-L/opt/spidermonkey-gnu/lib -L/opt/libwebp-gnu/lib'
go build -tags netgo,osusergo -ldflags='-s -w' -o /out/aistudio2api ./cmd/aistudio2api
