#!/bin/sh

set -eu

output_dir=${OUTPUT_DIR:-dist/linux-amd64}
version=${VERSION:-dev}
source_commit=${SOURCE_COMMIT:?SOURCE_COMMIT is required}
ldflags="-s -w -X github.com/duvu/ya-router/src.version=${version}"

mkdir -p "$output_dir"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$ldflags" -o "$output_dir/ya-router" ./cmd/ya-router
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$ldflags" -o "$output_dir/ya-routerd" ./cmd/ya-routerd
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$ldflags" -o "$output_dir/ya" ./cmd/ya
printf 'source_commit=%s\nversion=%s\nplatform=linux/amd64\n' "$source_commit" "$version" > "$output_dir/build-info.txt"
(cd "$output_dir" && sha256sum ya-router ya-routerd ya build-info.txt > checksums.txt)
