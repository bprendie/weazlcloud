#!/usr/bin/env bash
set -euo pipefail

measure_root="${WEAZLCLOUD_MEASURE_ROOT:-}"
if [[ -z "$measure_root" && -d /exports/dockervolume/weazlcloud ]]; then
  measure_root=/exports/dockervolume/weazlcloud/.weazl-measure
fi
measure_env=(WEAZLCLOUD_MEASURE=1)
if [[ -n "$measure_root" ]]; then
  measure_env+=(WEAZLCLOUD_MEASURE_ROOT="$measure_root")
fi
if [[ -x /usr/bin/time ]]; then
  exec /usr/bin/time -v env "${measure_env[@]}" go test ./internal/library -run '^TestTransferMeasurement$' -count=1 -v
fi
echo "warning: /usr/bin/time is unavailable; Go allocation data will be reported without peak RSS" >&2
exec env "${measure_env[@]}" go test ./internal/library -run '^TestTransferMeasurement$' -count=1 -v
