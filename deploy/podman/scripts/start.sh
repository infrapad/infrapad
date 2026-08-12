#!/usr/bin/env bash

set -eo pipefail

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

ensure_podman_ready

podman compose "$@" up
