#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
mkdir -p bin
exec go build -o bin/bb-pr .
