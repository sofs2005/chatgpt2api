#!/usr/bin/env sh
set -eu

usage() {
  cat <<'EOF'
Usage:
  sh deploy/docker-build-limited.sh [up|build]

Creates a resource-capped BuildKit builder, then builds the local Docker image.

Tunable environment variables:
  CHATGPT2API_BUILDER_NAME     Builder name (default: chatgpt2api-local-build)
  BUILD_CPUS                   Whole CPU cores available to BuildKit (default: 2)
  BUILD_MEMORY                 BuildKit memory limit (default: 2g)
  BUILD_MEMORY_SWAP            BuildKit memory+swap limit (default: BUILD_MEMORY)
  BUILDKIT_MAX_PARALLELISM     BuildKit solver parallelism (default: BUILD_CPUS)
  BUILD_GOMAXPROCS             Go compiler parallelism (default: BUILD_CPUS)
  BUILD_GOMEMLIMIT             Go soft memory limit (default: 1GiB)
  BUILD_NODE_OPTIONS           Node options for the web build
  BUILD_CPUSET_CPUS            Optional cpuset, for example 0-1

Examples:
  BUILD_CPUS=2 BUILD_MEMORY=2g sh deploy/docker-build-limited.sh up
  BUILD_CPUS=1 BUILD_MEMORY=1536m BUILD_GOMEMLIMIT=768MiB sh deploy/docker-build-limited.sh build
EOF
}

require_uint() {
  name="$1"
  value="$2"
  case "$value" in
    ''|*[!0-9]*)
      echo "$name must be a positive integer, got: $value" >&2
      exit 2
      ;;
    0)
      echo "$name must be greater than zero" >&2
      exit 2
      ;;
  esac
}

command="${1:-up}"
case "$command" in
  up|build)
    ;;
  -h|--help|help)
    usage
    exit 0
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)

builder_name="${CHATGPT2API_BUILDER_NAME:-chatgpt2api-local-build}"
build_cpus="${BUILD_CPUS:-2}"
build_cpu_period="${BUILD_CPU_PERIOD:-100000}"
build_memory="${BUILD_MEMORY:-2g}"
build_memory_swap="${BUILD_MEMORY_SWAP:-$build_memory}"
buildkit_max_parallelism="${BUILDKIT_MAX_PARALLELISM:-$build_cpus}"

require_uint BUILD_CPUS "$build_cpus"
require_uint BUILD_CPU_PERIOD "$build_cpu_period"
require_uint BUILDKIT_MAX_PARALLELISM "$buildkit_max_parallelism"

build_cpu_quota="${BUILD_CPU_QUOTA:-$((build_cpus * build_cpu_period))}"
require_uint BUILD_CPU_QUOTA "$build_cpu_quota"

export DOCKER_BUILDKIT=1
export BUILDX_BUILDER="$builder_name"
export BUILD_GOMAXPROCS="${BUILD_GOMAXPROCS:-$build_cpus}"
export BUILD_GOMEMLIMIT="${BUILD_GOMEMLIMIT:-1GiB}"
export BUILD_NODE_OPTIONS="${BUILD_NODE_OPTIONS:---max-old-space-size=1024}"
export CHATGPT2API_LOCAL_IMAGE="${CHATGPT2API_LOCAL_IMAGE:-chatgpt2api:local}"
export CHATGPT2API_VERSION="${CHATGPT2API_VERSION:-0.0.0-dev}"

require_uint BUILD_GOMAXPROCS "$BUILD_GOMAXPROCS"

cache_root="${XDG_CACHE_HOME:-${HOME:-.}/.cache}/chatgpt2api-buildkit"
mkdir -p "$cache_root"
buildkit_config="$cache_root/buildkitd.toml"
fingerprint_file="$cache_root/$builder_name.options"

cat > "$buildkit_config" <<EOF
[worker.oci]
  max-parallelism = $buildkit_max_parallelism
EOF

fingerprint="cpu-period=$build_cpu_period
cpu-quota=$build_cpu_quota
memory=$build_memory
memory-swap=$build_memory_swap
max-parallelism=$buildkit_max_parallelism
cpuset-cpus=${BUILD_CPUSET_CPUS:-}"

create_builder() {
  if [ -n "${BUILD_CPUSET_CPUS:-}" ]; then
    docker buildx create \
      --name "$builder_name" \
      --driver docker-container \
      --driver-opt "image=moby/buildkit:buildx-stable-1" \
      --driver-opt "cpu-period=$build_cpu_period" \
      --driver-opt "cpu-quota=$build_cpu_quota" \
      --driver-opt "memory=$build_memory" \
      --driver-opt "memory-swap=$build_memory_swap" \
      --driver-opt "cpuset-cpus=$BUILD_CPUSET_CPUS" \
      --buildkitd-config "$buildkit_config" \
      --use \
      --bootstrap >/dev/null
  else
    docker buildx create \
      --name "$builder_name" \
      --driver docker-container \
      --driver-opt "image=moby/buildkit:buildx-stable-1" \
      --driver-opt "cpu-period=$build_cpu_period" \
      --driver-opt "cpu-quota=$build_cpu_quota" \
      --driver-opt "memory=$build_memory" \
      --driver-opt "memory-swap=$build_memory_swap" \
      --buildkitd-config "$buildkit_config" \
      --use \
      --bootstrap >/dev/null
  fi
  printf '%s' "$fingerprint" > "$fingerprint_file"
}

if docker buildx inspect "$builder_name" >/dev/null 2>&1; then
  if [ ! -f "$fingerprint_file" ] || [ "$(cat "$fingerprint_file")" != "$fingerprint" ]; then
    docker buildx rm --keep-state "$builder_name" >/dev/null 2>&1 || docker buildx rm "$builder_name" >/dev/null
    create_builder
  else
    docker buildx use "$builder_name" >/dev/null
    docker buildx inspect --bootstrap "$builder_name" >/dev/null
  fi
else
  create_builder
fi

docker buildx build \
  --builder "$builder_name" \
  --load \
  --tag "$CHATGPT2API_LOCAL_IMAGE" \
  --file "$repo_root/deploy/Dockerfile" \
  --build-arg "VERSION=$CHATGPT2API_VERSION" \
  --build-arg "BUILD_GOMAXPROCS=$BUILD_GOMAXPROCS" \
  --build-arg "BUILD_GOMEMLIMIT=$BUILD_GOMEMLIMIT" \
  --build-arg "BUILD_NODE_OPTIONS=$BUILD_NODE_OPTIONS" \
  "$repo_root"

if [ "$command" = "up" ]; then
  CHATGPT2API_DATA_DIR="$repo_root/data" \
  CHATGPT2API_ENV_FILE="$repo_root/.env" \
  CHATGPT2API_IMAGE="$CHATGPT2API_LOCAL_IMAGE" \
  CHATGPT2API_PULL_POLICY=never \
  docker compose --env-file "$repo_root/.env" -f "$repo_root/deploy/docker-compose.yml" up -d --no-build
fi
