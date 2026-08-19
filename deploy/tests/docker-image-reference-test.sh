#!/bin/bash

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)

files=(
    "$ROOT_DIR/deploy/.env.example"
    "$ROOT_DIR/deploy/APPLE_CONTAINER.md"
    "$ROOT_DIR/deploy/DOCKER.md"
    "$ROOT_DIR/deploy/apple-container.sh"
    "$ROOT_DIR/deploy/build_image.sh"
    "$ROOT_DIR/deploy/docker-compose.local.yml"
    "$ROOT_DIR/deploy/docker-compose.standalone.yml"
    "$ROOT_DIR/deploy/docker-compose.yml"
    "$ROOT_DIR/frontend/src/components/common/VersionBadge.vue"
)

for file in "${files[@]}"; do
    if grep -Fq 'weishaw/sub2api' "$file"; then
        echo "old Docker Hub image remains in $file" >&2
        exit 1
    fi
done

grep -Fq 'forever94yu/sub2api:latest' "$ROOT_DIR/deploy/.env.example"
grep -Fq 'forever94yu/sub2api:latest' "$ROOT_DIR/deploy/docker-compose.yml"
grep -Fq 'forever94yu/sub2api:latest' "$ROOT_DIR/deploy/docker-compose.local.yml"
grep -Fq 'forever94yu/sub2api:latest' "$ROOT_DIR/deploy/docker-compose.standalone.yml"
grep -Fq 'forever94yu/sub2api:latest' "$ROOT_DIR/deploy/build_image.sh"
grep -Fq "const DOCKER_IMAGE = 'forever94yu/sub2api'" "$ROOT_DIR/frontend/src/components/common/VersionBadge.vue"

echo "Docker image reference checks passed"
