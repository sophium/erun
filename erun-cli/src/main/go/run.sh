#!/usr/bin/env sh

set -eu

cd "$(dirname "$0")"

go build .

./erun "$@"