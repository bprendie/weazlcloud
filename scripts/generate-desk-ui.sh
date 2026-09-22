#!/usr/bin/env bash
set -euo pipefail

source_dir="mockup-ui"
generated_dir="internal/desk/ui"

if [[ ! -d "$source_dir" ]]; then
  echo "missing UI source directory: $source_dir" >&2
  exit 1
fi

mkdir -p "$generated_dir"
find "$generated_dir" -mindepth 1 -delete
cp -a "$source_dir"/. "$generated_dir"/
