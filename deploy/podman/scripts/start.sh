#!/usr/bin/env bash

set -eo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

ensure_podman_ready() {
  # On Linux, the podman API socket is managed by a systemd user unit.
  # Start it if it isn't active — docker-compose needs it.
  if [ "$(uname -s)" = "Linux" ] && command -v systemctl &>/dev/null; then
    if ! systemctl --user is-active podman.socket &>/dev/null; then
      echo "ERROR: Podman API socket is not running. docker-compose needs it to communicate with podman." >&2
      echo "  Run: systemctl --user enable --now podman.socket" >&2
      return 1
    fi
  fi

  # Detect the socket path if not explicitly set.
  if [ -z "${PODMAN_SOCKET:-}" ]; then
    PODMAN_SOCKET=$(podman info --format '{{.Host.RemoteSocket.Path}}' 2>/dev/null | sed 's|^unix://||') || true
    if [ -z "$PODMAN_SOCKET" ] && [ "$(uname -s)" = "Linux" ]; then
      PODMAN_SOCKET="/run/user/$(id -u)/podman/podman.sock"
    fi
  fi

  if [ -z "$PODMAN_SOCKET" ]; then
    echo "ERROR: Could not determine podman socket path. Is podman running?" >&2
    return 1
  fi

  # On Linux the socket is a local file we can verify. On macOS it lives
  # inside the podman VM — podman info reports the VM-internal path, which
  # is correct for container volume mounts but doesn't exist on the host.
  if [ "$(uname -s)" = "Linux" ] && [ ! -S "$PODMAN_SOCKET" ]; then
    echo "ERROR: Podman API socket not found at $PODMAN_SOCKET" >&2
    echo "  Run: systemctl --user enable --now podman.socket" >&2
    return 1
  fi

  export PODMAN_SOCKET

  # On Linux, set DOCKER_HOST so docker-compose can find the podman socket.
  # On macOS, podman compose sets this automatically — overriding it with the
  # VM-internal path would break docker-compose.
  if [ "$(uname -s)" = "Linux" ] && [ -z "${DOCKER_HOST:-}" ]; then
    export DOCKER_HOST="unix://${PODMAN_SOCKET}"
  fi

}

# Split arguments at "--": before goes to compose, after goes to up.
compose_args=()
up_args=()
past_separator=false
for arg in "$@"; do
  if [[ "$arg" == "--" ]]; then
    past_separator=true
    continue
  fi
  if $past_separator; then
    up_args+=("$arg")
  else
    compose_args+=("$arg")
  fi
done

# Authentication only applies to the combined environment and the focused
# authentication proxy compositions. Other focused compositions ignore
# INFRAPAD_AUTH.
auth_composition=""
expect_compose_file=false
for arg in "${compose_args[@]}"; do
  if $expect_compose_file; then
    compose_file="${arg##*/}"
    expect_compose_file=false
  else
    case "$arg" in
      -f|--file)
        expect_compose_file=true
        continue
        ;;
      --file=*)
        compose_file="${arg#--file=}"
        compose_file="${compose_file##*/}"
        ;;
      *)
        continue
        ;;
    esac
  fi

  case "$compose_file" in
    all.yaml)
      auth_composition="all"
      ;;
    openshift-oauth-proxy.yaml)
      if [[ "$auth_composition" != "all" ]]; then
        auth_composition="oauth-proxy"
      fi
      ;;
    dummy-auth-proxy.yaml)
      if [[ "$auth_composition" != "all" ]]; then
        auth_composition="dummy-auth-proxy"
      fi
      ;;
  esac
done

case "$auth_composition" in
  all)
    auth_mode="${INFRAPAD_AUTH:-dummy}"
    ;;
  oauth-proxy)
    auth_mode="openshift"
    ;;
  dummy-auth-proxy)
    auth_mode="dummy"
    ;;
  *)
    auth_mode="none"
    ;;
esac

case "$auth_mode" in
  none|openshift|dummy)
    ;;
  *)
    echo "ERROR: Invalid INFRAPAD_AUTH value '$auth_mode'; accepted values: none, openshift, dummy." >&2
    exit 1
    ;;
esac

# UI mode only applies when an authentication composition is active. This
# deliberately leaves focused services such as PostgreSQL independent from UI
# development settings in the caller's environment.
if [[ -n "$auth_composition" ]]; then
  ui_mode="${INFRAPAD_UI:-}"
  case "$ui_mode" in
    ""|prod)
      mode_upstream="http://127.0.0.1:8088"
      ;;
    hot-reload)
      if [[ "$auth_mode" == "none" ]]; then
        echo "ERROR: INFRAPAD_UI=hot-reload requires an authentication proxy; INFRAPAD_AUTH=none is not supported by the standalone UI." >&2
        exit 1
      fi
      mode_upstream="http://127.0.0.1:5173"
      ;;
    *)
      echo "ERROR: Invalid INFRAPAD_UI value '$ui_mode'; accepted values: unset, empty, prod, hot-reload." >&2
      exit 1
      ;;
  esac

  # An explicit upstream is an advanced override for either proxy mode.
  if [[ -z "${INFRAPAD_AUTH_UPSTREAM:-}" ]]; then
    export INFRAPAD_AUTH_UPSTREAM="$mode_upstream"
  fi
fi

case "$auth_mode" in
  openshift)
    "$SCRIPT_DIR/openshift-oauth-bootstrap.sh"
    compose_args+=("--profile" "auth-openshift")
    ;;
  dummy)
    compose_args+=("--profile" "auth-dummy")
    ;;
esac

ensure_podman_ready

# In foreground mode, abort all containers when any one exits so failures
# are immediately visible instead of silently ignored.
if [[ ! " ${up_args[*]} " =~ " -d " ]] && [[ ! " ${up_args[*]} " =~ " --detach " ]]; then
  up_args+=("--abort-on-container-exit")
fi

podman compose "${compose_args[@]}" up "${up_args[@]}"
