#!/usr/bin/env sh
set -eu
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
exec "${LANBRIDGE_PYTHON:-python3}" "$project_dir/run.py" "$@"
