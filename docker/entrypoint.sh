#!/bin/sh
set -e

# Require credentials when starting with the web interface.
if [ -z "$PIPELINER_WEB_USER" ] || [ -z "$PIPELINER_WEB_PASSWORD" ]; then
  echo "error: PIPELINER_WEB_USER and PIPELINER_WEB_PASSWORD must be set" >&2
  exit 1
fi

# Create an empty config on first run if the file does not exist yet.
CONFIG_FILE="${PIPELINER_CONFIG:-/config/config.yml}"
if [ ! -f "$CONFIG_FILE" ]; then
  mkdir -p "$(dirname "$CONFIG_FILE")"
  touch "$CONFIG_FILE"
fi

exec /app/pipeliner daemon \
  --config  "${PIPELINER_CONFIG:-/config/config.yml}" \
  --web     "${PIPELINER_WEB_ADDR:-:8080}" \
  --web-user     "$PIPELINER_WEB_USER" \
  --web-password "$PIPELINER_WEB_PASSWORD" \
  ${PIPELINER_LOG_LEVEL:+--log-level "$PIPELINER_LOG_LEVEL"} \
  "$@"
