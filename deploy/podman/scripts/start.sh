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
# OAuth proxy composition. Other focused compositions ignore INFRAPAD_AUTH.
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
    oauth-proxy.yaml)
      if [[ "$auth_composition" != "all" ]]; then
        auth_composition="oauth-proxy"
      fi
      ;;
  esac
done

case "$auth_composition" in
  all)
    auth_mode="${INFRAPAD_AUTH:-none}"
    ;;
  oauth-proxy)
    auth_mode="openshift"
    ;;
  *)
    auth_mode="none"
    ;;
esac

case "$auth_mode" in
  none)
    ;;
  openshift)
    "$SCRIPT_DIR/openshift-oauth-bootstrap.sh"
    compose_args+=("--profile" "auth-openshift")
    ;;
  dummy)
    echo "ERROR: INFRAPAD_AUTH=dummy is not implemented yet; use 'none' or 'openshift'." >&2
    exit 1
    ;;
  *)
    echo "ERROR: Invalid INFRAPAD_AUTH value '$auth_mode'; accepted values: none, openshift, dummy." >&2
    exit 1
    ;;
esac

ensure_podman_ready

# In foreground mode, abort all containers when any one exits so failures
# are immediately visible instead of silently ignored.
if [[ ! " ${up_args[*]} " =~ " -d " ]] && [[ ! " ${up_args[*]} " =~ " --detach " ]]; then
  up_args+=("--abort-on-container-exit")
fi

podman compose "${compose_args[@]}" up "${up_args[@]}"
