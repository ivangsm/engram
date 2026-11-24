#!/bin/sh
set -e

# Support direct invocation of engram, engramd, sh, or any explicit command
if [ "$1" = "engram" ] || [ "$1" = "engramd" ] || [ "$1" = "sh" ]; then
    exec "$@"
fi

# If argument starts with a dash/flag, route directly to engramd
if [ "${1#-}" != "$1" ]; then
    exec engramd "$@"
fi

# If no arguments provided, default to engramd
if [ $# -eq 0 ]; then
    exec engramd
fi

exec "$@"
