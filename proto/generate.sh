#!/bin/sh
set -eu
command -v protoc >/dev/null
command -v protoc-gen-go >/dev/null
command -v protoc-gen-go-grpc >/dev/null
cd "$(dirname "$0")"
protoc --go_out=paths=source_relative:. --go-grpc_out=paths=source_relative:. externalgrpc.proto
