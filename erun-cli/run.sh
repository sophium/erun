#!/usr/bin/env sh

set -eu

cd "$(dirname "$0")"

mkdir -p bin

go build -o bin/erun ./src/main/go

./bin/erun "$@"
