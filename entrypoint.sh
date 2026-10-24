#!/bin/sh
set -e
# Docker creates a missing bind source as root, so the data dir commonly arrives
# owned by root. Fix it once while we are root, then drop to app for the process.
if [ "$(id -u)" = "0" ]; then
	chown -R app:app /data 2>/dev/null || true
	exec su-exec app statsbot "$@"
fi
exec statsbot "$@"
