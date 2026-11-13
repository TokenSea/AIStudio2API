#!/bin/sh
set -eu
# Run inside Dockerfile.test's GNU Linux image, with network disabled.
export GOMAXPROCS=2 GOMEMLIMIT=768MiB CGO_ENABLED=1
export CC=gcc CXX=g++
export CGO_CPPFLAGS='-I/opt/spidermonkey-gnu/include -I/opt/libwebp-gnu/include'
export CGO_LDFLAGS='-L/opt/spidermonkey-gnu/lib -L/opt/libwebp-gnu/lib'
go test -p=1 ./internal/managed ./internal/api ./internal/aistudio -count=1
if [ "${IORUN_APP_TESTS:-0}" = 1 ]; then
  go test -p=1 -tags netgo,osusergo ./internal/app -run '^TestIORun' -count=1
fi
