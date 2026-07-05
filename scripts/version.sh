#!/usr/bin/env sh
set -eu

tag="$(git describe --tags --abbrev=0 2>/dev/null || true)"
sha="$(git rev-parse --short=7 HEAD 2>/dev/null || echo unknown)"

if [ -n "$tag" ]; then
  base="${tag#v}"
  major="${base%%.*}"
  rest="${base#*.}"
  minor="${rest%%.*}"
  patch="${base##*.}"
  next_patch=$((patch + 1))
  count="$(git rev-list "$tag"..HEAD --count 2>/dev/null || echo 0)"
  printf "%s.%s.%s-dev.%s+g%s\n" "$major" "$minor" "$next_patch" "$count" "$sha"
else
  count="$(git rev-list HEAD --count 2>/dev/null || echo 0)"
  printf "0.1.0-dev.%s+g%s\n" "$count" "$sha"
fi
