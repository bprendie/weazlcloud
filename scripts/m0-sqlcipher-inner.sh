#!/bin/sh
set -eu
apk add --no-cache coreutils sqlcipher >/dev/null

db=/work/vault-bobp.db
backup=/work/vault-bobp.backup.db
other=/work/vault-other.db
key='m0-disposable-key-20260928'
rekey='m0-disposable-rekey-20260928'
other_key='m0-other-vault-key-20260928'
sentinel='WEAZL_M0_PRIVATE_SENTINEL_20260928'
now_ns() { date +%s%N; }
run_sql() { sqlcipher "$db"; }

start=$(now_ns)
run_sql <<SQL >/work/setup.out
PRAGMA key = '$key';
PRAGMA cipher_memory_security = ON;
PRAGMA journal_mode = WAL;
PRAGMA synchronous = FULL;
PRAGMA temp_store = MEMORY;
CREATE TABLE assets (
  id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, path TEXT NOT NULL,
  media_type TEXT NOT NULL, capture_unix INTEGER, capture_source TEXT,
  capture_offset_min INTEGER, imported_unix INTEGER NOT NULL,
  mtime_unix INTEGER NOT NULL, width INTEGER, height INTEGER,
  favorite INTEGER NOT NULL DEFAULT 0, source_root TEXT NOT NULL,
  revision INTEGER NOT NULL, crash_marker INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE albums (
  id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '', cover_asset_id TEXT
);
CREATE TABLE album_assets (
  album_id TEXT NOT NULL, asset_id TEXT NOT NULL, position INTEGER NOT NULL,
  PRIMARY KEY (album_id, asset_id)
);
CREATE TABLE derivative_jobs (
  owner_id TEXT NOT NULL, asset_id TEXT NOT NULL, revision INTEGER NOT NULL,
  kind TEXT NOT NULL, renderer TEXT NOT NULL, status TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (asset_id, revision, kind, renderer)
);
CREATE INDEX assets_timeline ON assets(owner_id, media_type, capture_unix DESC, id DESC);
CREATE INDEX assets_root_timeline ON assets(owner_id, source_root, media_type, capture_unix DESC, id DESC);
CREATE INDEX assets_favorites ON assets(owner_id, favorite, media_type, capture_unix DESC, id DESC);
CREATE INDEX album_assets_order ON album_assets(album_id, position, asset_id);
CREATE INDEX derivative_jobs_ready ON derivative_jobs(owner_id, status, kind, asset_id);
WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 97000)
INSERT INTO assets
SELECT printf('asset-%06d', i), 'owner-bobp',
 CASE WHEN i <= 32000 THEN printf('Photos/%04d/album-%03d/photo-%06d.jpg',2010+(i%17),i%400,i)
      ELSE printf('Documents/%04d/file-%06d.bin',2020+(i%7),i) END,
 CASE WHEN i <= 32000 THEN 'image/jpeg' ELSE 'application/octet-stream' END,
 CASE WHEN i <= 32000 AND i%113 != 0
      THEN 1262304000+((i%17)*31536000)+((i%365)*86400)+(i%86400) END,
 CASE WHEN i%7=0 THEN 'takeout-photoTakenTime'
      WHEN i%5=0 THEN 'exif-DateTimeOriginal' END,
 CASE WHEN i%11=0 THEN -300 WHEN i%13=0 THEN 60 END,
 1790553600+(i%86400), 1790553600+(i%31)*3600,
 CASE WHEN i<=32000 THEN 4032 END, CASE WHEN i<=32000 THEN 3024 END,
 CASE WHEN i%97=0 THEN 1 ELSE 0 END,
 CASE WHEN i<=32000 THEN '/Photos' ELSE '/' END, 1, 0
FROM n;
WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i < 4)
INSERT INTO albums SELECT printf('album-%03d',i),'owner-bobp',printf('Album %03d',i),'',printf('asset-%06d',i+1) FROM n;
WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i < 4999)
INSERT INTO album_assets SELECT printf('album-%03d',i%5),printf('asset-%06d',(i%32000)+1),i FROM n;
WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 10000)
INSERT INTO derivative_jobs
SELECT 'owner-bobp',printf('asset-%06d',i),1,CASE WHEN i%3=0 THEN 'grid' ELSE 'viewer' END,
'raster-v3',CASE WHEN i%3=0 THEN 'ready' ELSE 'queued' END,0 FROM n;
INSERT INTO assets VALUES ('sentinel','owner-bobp','$sentinel','image/jpeg',1,'test',0,1790553600,1790553600,1,1,0,'/Photos',1,0);
DELETE FROM assets WHERE id='sentinel';
CREATE TEMP TABLE temp_secret(value TEXT);
INSERT INTO temp_secret VALUES ('$sentinel');
SELECT count(*) FROM temp_secret;
PRAGMA wal_checkpoint(TRUNCATE);
SQL
end=$(now_ns)
printf 'fixture_setup_ms=%s\n' "$(( (end-start)/1000000 ))"

printf 'fixture_entries='; run_sql <<SQL
PRAGMA key='$key'; SELECT count(*) FROM assets;
SQL
printf 'fixture_photo_assets='; run_sql <<SQL
PRAGMA key='$key'; SELECT count(*) FROM assets WHERE media_type LIKE 'image/%';
SQL
printf 'fixture_missing_capture='; run_sql <<SQL
PRAGMA key='$key'; SELECT count(*) FROM assets WHERE media_type LIKE 'image/%' AND capture_unix IS NULL;
SQL
printf 'fixture_capture_years='; run_sql <<SQL
PRAGMA key='$key'; SELECT min(strftime('%Y',capture_unix,'unixepoch')),max(strftime('%Y',capture_unix,'unixepoch')) FROM assets WHERE capture_unix IS NOT NULL;
SQL
printf 'fixture_import_years='; run_sql <<SQL
PRAGMA key='$key'; SELECT min(strftime('%Y',imported_unix,'unixepoch')),max(strftime('%Y',imported_unix,'unixepoch')) FROM assets;
SQL

write_query() {
  name=$1
  shift
  printf '%s\n' "PRAGMA key='$key';" "$@" > "/work/$name.sql"
}
write_query timeline "SELECT id,path,capture_unix FROM assets WHERE owner_id='owner-bobp' AND source_root='/Photos' AND media_type LIKE 'image/%' ORDER BY capture_unix DESC,id DESC LIMIT 100;"
write_query date_bucket "SELECT strftime('%Y-%m',capture_unix,'unixepoch'),count(*) FROM assets WHERE owner_id='owner-bobp' AND media_type LIKE 'image/%' AND capture_unix IS NOT NULL GROUP BY 1 ORDER BY 1 DESC;"
write_query album_page "SELECT a.id,a.path,aa.position FROM album_assets aa JOIN assets a ON a.id=aa.asset_id WHERE aa.album_id='album-001' ORDER BY aa.position,aa.asset_id LIMIT 100;"
write_query mixed_write "BEGIN IMMEDIATE; UPDATE assets SET favorite=1,revision=revision+1 WHERE owner_id='owner-bobp' AND id IN ('asset-000001','asset-000002'); INSERT OR REPLACE INTO derivative_jobs VALUES ('owner-bobp','asset-000001',2,'grid','raster-v3','queued',0); COMMIT; SELECT count(*) FROM derivative_jobs WHERE status='queued';"

measure() {
  label=$1
  query=$2
  values=''
  i=0
  while [ "$i" -lt 20 ]; do
    start=$(now_ns)
    sqlcipher "$db" < "/work/$query.sql" >/work/measure.out
    end=$(now_ns)
    values="$values $(( (end-start)/1000 ))"
    i=$((i+1))
  done
  sorted=$(printf '%s\n' $values | sort -n)
  count=$(printf '%s\n' "$sorted" | wc -l)
  min=$(printf '%s\n' "$sorted" | sed -n '1p')
  median=$(printf '%s\n' "$sorted" | sed -n "$(( (count+1)/2 ))p")
  p95=$(printf '%s\n' "$sorted" | sed -n "$(( (count*95+99)/100 ))p")
  max=$(printf '%s\n' "$sorted" | sed -n "$count""p")
  printf '%s_us_min=%s_median=%s_p95=%s_max=%s\n' "$label" "$min" "$median" "$p95" "$max"
}
measure timeline timeline
measure date_bucket date_bucket
measure album_page album_page
measure mixed_write mixed_write

resource_start=$(now_ns)
sqlcipher "$db" < /work/date_bucket.sql >/dev/null &
resource_pid=$!
resource_rss=0
resource_ticks=0
while kill -0 "$resource_pid" 2>/dev/null; do
  current_rss=$(awk '/VmRSS:/ {print $2}' "/proc/$resource_pid/status" 2>/dev/null || printf '0')
  [ "${current_rss:-0}" -gt "$resource_rss" ] && resource_rss=$current_rss
  current_ticks=$(awk '{print $14+$15}' "/proc/$resource_pid/stat" 2>/dev/null || printf '0')
  [ "${current_ticks:-0}" -gt "$resource_ticks" ] && resource_ticks=$current_ticks
  sleep 0.01
done
wait "$resource_pid"
resource_end=$(now_ns)
printf 'sqlcipher_resource_elapsed_ms=%s_sqlcipher_cpu_ticks=%s_sqlcipher_peak_rss_kib=%s\n' \
  "$(( (resource_end-resource_start)/1000000 ))" "$resource_ticks" "$resource_rss"

sqlcipher "$db" <<SQL >/dev/null
PRAGMA key='$key';
PRAGMA wal_checkpoint(TRUNCATE);
ATTACH DATABASE '$backup' AS backup KEY '$key';
SELECT sqlcipher_export('backup');
DETACH DATABASE backup;
SQL
reopened=$(sqlcipher "$backup" <<SQL
PRAGMA key='$key'; PRAGMA cipher_integrity_check; SELECT count(*) FROM assets;
SQL
)
printf 'backup_reopen=%s\n' "$(printf '%s' "$reopened" | tr '\n' '|')"

printf '%s\n' "PRAGMA key='$key';" 'BEGIN IMMEDIATE;' 'UPDATE assets SET crash_marker=1;' '.shell sleep 2' 'COMMIT;' > /work/crash.sql
set +e
timeout --signal=KILL 0.5s sqlcipher "$db" < /work/crash.sql >/work/crash.out 2>/work/crash.err
crash_status=$?
set -e
printf 'crash_writer_status=%s\n' "$crash_status"
crash_marker=$(run_sql <<SQL
PRAGMA key='$key'; PRAGMA cipher_integrity_check; SELECT count(*) FROM assets WHERE crash_marker=1;
SQL
)
crash_normalized=$(printf '%s' "$crash_marker" | tr '\n' '|' | sed 's/|$//')
printf 'crash_reopen=%s\n' "$crash_normalized"
[ "$crash_normalized" = 'ok|0' ] || { echo 'crash_recovery_failed=true' >&2; exit 1; }

cp "$backup" "$other"
sqlcipher "$other" <<SQL >/dev/null
PRAGMA key='$key'; PRAGMA rekey='$other_key'; PRAGMA cipher_integrity_check;
SQL
sqlcipher "$db" <<SQL >/work/rekey.out
PRAGMA key='$key'; PRAGMA rekey='$rekey'; PRAGMA cipher_integrity_check;
SQL
rekey_read=$(sqlcipher "$db" <<SQL
PRAGMA key='$rekey'; SELECT count(*) FROM assets;
SQL
)
printf 'rekey_read=%s\n' "$rekey_read"

set +e
old_read=$(sqlcipher "$db" <<SQL 2>/work/old-key.err
PRAGMA key='$key'; SELECT count(*) FROM assets;
SQL
)
old_status=$?
other_read=$(sqlcipher "$other" <<SQL 2>/dev/null
PRAGMA key='$rekey'; SELECT count(*) FROM assets;
SQL
)
other_status=$?
set -e
printf 'old_key_status=%s output=%s\n' "$old_status" "$(printf '%s' "$old_read" | tr '\n' '|')"
printf 'cross_vault_key_status=%s output=%s\n' "$other_status" "$(printf '%s' "$other_read" | tr '\n' '|')"
[ "$old_status" -ne 0 ] || [ -z "$old_read" ] || { echo 'old_key_unexpectedly_opened=true' >&2; exit 1; }
[ "$other_status" -ne 0 ] || [ -z "$other_read" ] || { echo 'cross_vault_key_unexpectedly_opened=true' >&2; exit 1; }

if grep -R -a -q "$sentinel" /work; then
  echo 'plaintext_sentinel_found=true'
  exit 1
fi
echo 'old_key_rejected=true'
echo 'cross_vault_key_rejected=true'
echo 'plaintext_sentinel_found=false'
echo 'encrypted_reopen=true'
echo 'encrypted_backup=true'
echo 'encrypted_rekey=true'
