#!/usr/bin/env sh
set -eu

usage() {
  cat <<'EOF'
Usage:
  sh deploy/docker-stack.sh <command> [args...]

One entry point for every compose operation, so the long
"--env-file .env -f deploy/... " prefix never has to be typed or remembered.

Commands:
  up                   Start (or recreate) the stack in the background.
  update               Pull the image, then up. The usual "get the latest and
                       restart" command.
  pull                 Pull the image only.
  restart              Restart the running stack without recreating containers.
  down                 Stop and remove the stack.
  logs [args...]       Follow the app logs. Extra args pass through,
                       e.g. "logs --tail 100".
  ps                   Show container status.
  config               Print the resolved compose configuration.
  check                Diagnose the image actually in use. Start here when a
                       CHATGPT2API_IMAGE change appears to have no effect.
  use <stack>          Remember the stack to use for later commands.
  stack                Print the stack currently in use.

Picking the stack (resolution order, first hit wins):
  1. --stack <stack> on the command line
  2. CHATGPT2API_STACK environment variable
  3. the value saved by "use" (deploy/.stack)
  4. base

  Stack names: base | flaresolverr | warp
  - base          only the app; egress behavior unchanged.
  - flaresolverr  adds the cf_clearance fallback; egress unchanged.
  - warp          routes upstream traffic through WARP and adds the fallback.

Examples:
  sh deploy/docker-stack.sh use warp     # choose once, remember it
  sh deploy/docker-stack.sh update       # pull + up from then on
  sh deploy/docker-stack.sh logs
  sh deploy/docker-stack.sh check
  sh deploy/docker-stack.sh --stack base up

Why this script exists:
  Compose reads the .env used for ${...} interpolation from the PROJECT
  DIRECTORY, which is the directory of the first -f file -- that is deploy/,
  not the repository root. The env_file: ../.env entry only injects variables
  INTO the container and plays no part in interpolation. So omitting
  "--env-file .env" makes compose silently fall back to the ${VAR:-default}
  values in the compose file: the stack starts, but on the default image.
  This script always passes the repository-root .env explicitly.
EOF
}

die() {
  echo "error: $*" >&2
  exit 1
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
env_file="$repo_root/.env"
stack_file="$repo_root/deploy/.stack"

# --- stack selection -------------------------------------------------------

stack_from_name() {
  case "$1" in
    base) echo "docker-compose.yml" ;;
    flaresolverr) echo "docker-compose.flaresolverr.yml" ;;
    warp) echo "docker-compose.warp.yml" ;;
    *) die "unknown stack: $1 (expected base, flaresolverr or warp)" ;;
  esac
}

saved_stack() {
  # Older files may end with a CR; strip it so a file written on Windows
  # does not produce a name with a trailing carriage return.
  if [ -f "$stack_file" ]; then
    tr -d '\r\n' < "$stack_file"
  fi
}

stack_arg=""
if [ "${1:-}" = "--stack" ]; then
  [ $# -ge 2 ] || die "--stack needs a value"
  stack_arg="$2"
  shift 2
fi

command="${1:-}"
[ -n "$command" ] || { usage >&2; exit 2; }
shift

case "$command" in
  -h|--help|help)
    usage
    exit 0
    ;;
esac

# "use" is the only command that does not need a resolved stack.
if [ "$command" = "use" ]; then
  [ $# -ge 1 ] || die "use needs a stack name"
  stack_from_name "$1" >/dev/null
  printf '%s\n' "$1" > "$stack_file"
  echo "stack set to: $1"
  exit 0
fi

stack_name="$stack_arg"
if [ -z "$stack_name" ]; then
  stack_name="$(saved_stack)"
fi
if [ -z "$stack_name" ]; then
  stack_name="${CHATGPT2API_STACK:-base}"
fi

compose_rel="deploy/$(stack_from_name "$stack_name")"
compose_abs="$repo_root/$compose_rel"

if [ "$command" = "stack" ]; then
  echo "$stack_name ($compose_rel)"
  exit 0
fi

# --- preflight ------------------------------------------------------------

command -v docker >/dev/null 2>&1 || die "docker not found in PATH"
[ -f "$compose_abs" ] || die "compose file not found: $compose_rel"

case "$command" in
  config|check) ;;
  *)
    # config/check are useful diagnostics even before .env exists; every real
    # operation depends on it.
    [ -f "$env_file" ] || die "$env_file not found; copy .env.example to .env first"
    ;;
esac

# Run from the repository root with a relative -f path: this is exactly the
# invocation the README documents, so relative paths inside the compose file
# (env_file, volumes, the WARP config) resolve the same way.
cd "$repo_root"

compose() {
  docker compose --env-file .env -f "$compose_rel" "$@"
}

# --- commands -------------------------------------------------------------

resolved_image() {
  compose config 2>/dev/null | awk '/image:/ { print $2; exit }'
}

running_image() {
  docker inspect chatgpt2api --format '{{.Config.Image}}' 2>/dev/null || true
}

case "$command" in
  up)
    compose up -d
    ;;
  update)
    compose pull
    compose up -d
    ;;
  pull)
    compose pull
    ;;
  restart)
    compose restart
    ;;
  down)
    compose down
    ;;
  logs)
    compose logs -f app "$@"
    ;;
  ps)
    compose ps
    ;;
  config)
    compose config
    ;;
  check)
    echo "repo root   : $repo_root"
    echo "compose file: $compose_rel"
    if [ -f "$env_file" ]; then
      echo "env file    : .env (found)"
    else
      echo "env file    : .env (MISSING - compose will use the defaults in the compose file)"
    fi
    echo
    if [ -f "$env_file" ]; then
      echo "CHATGPT2API_IMAGE in .env:"
      # Show the line as written; a typo here is the usual root cause.
      grep -n '^CHATGPT2API_IMAGE=' .env | cat -A || echo "  (not set)"
    fi
    echo
    echo "image compose resolves to:"
    echo "  $(resolved_image)"
    echo
    echo "image the running container uses:"
    echo "  $(running_image)"
    echo
    if [ "$(resolved_image)" = "$(running_image)" ]; then
      echo "OK: the running container matches the resolved configuration."
    else
      echo "MISMATCH: the container differs from the resolved configuration."
      echo "         Run 'sh deploy/docker-stack.sh update' to recreate it."
    fi
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac
