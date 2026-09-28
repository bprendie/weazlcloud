#!/usr/bin/env bash
set -euo pipefail
bash scripts/generate-desk-ui.sh
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cc -O3 -Wall -Wextra -Werror -o "$work/weazl-preview-turbo" native/preview/main.c -ljpeg
PATH="$work:$PATH" go test -race ./internal/library -run 'Test(Turbo|PreviewRenderer|MakeThumbnail|Preview.*Cancel)'
python3 scripts/test_native_preview.py "$work/weazl-preview-turbo"
# AddressSanitizer reserves a large virtual address range; only its AS ceiling is disabled.
cc -O1 -g -Wall -Wextra -Werror -DWEAZL_SANITIZE -fsanitize=address,undefined -fno-omit-frame-pointer -o "$work/preview-sanitize" native/preview/main.c -ljpeg
ASAN_OPTIONS=detect_leaks=0 python3 scripts/test_native_preview.py "$work/preview-sanitize"
