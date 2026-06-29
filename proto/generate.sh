#!/bin/sh
set -eu

check_version() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf '%s: expected "%s", actual "not found"\n' "$1" "$2" >&2
    exit 1
  fi
  if ! actual=$("$1" --version 2>&1); then
    printf '%s: expected "%s", actual "%s"\n' "$1" "$2" "$actual" >&2
    exit 1
  fi
  if [ "$actual" != "$2" ]; then
    printf '%s: expected "%s", actual "%s"\n' "$1" "$2" "$actual" >&2
    exit 1
  fi
}

check_version protoc 'libprotoc 6.33.0'
check_version protoc-gen-go 'protoc-gen-go v1.36.6'
check_version protoc-gen-go-grpc 'protoc-gen-go-grpc 1.5.1'

cd "$(dirname "$0")"
proto_dir=$PWD
source=cloudprovider/externalgrpc/protos/externalgrpc.proto
tmp=$(mktemp -d "${TMPDIR:-/tmp}/externalgrpc.XXXXXX")
cleanup() { rm -rf "$tmp"; }
trap cleanup 0
trap 'exit 1' HUP INT TERM

mkdir -p "$tmp/cloudprovider/externalgrpc/protos"
cp externalgrpc.proto "$tmp/$source"
cd "$tmp"
protoc --go_out=paths=source_relative:. --go-grpc_out=paths=source_relative:. "$source"
cp "${source%.proto}.pb.go" "${source%.proto}_grpc.pb.go" "$proto_dir/"
