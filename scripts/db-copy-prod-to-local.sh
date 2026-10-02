#!/usr/bin/env bash
# Replaces the local database with a copy of production's app data.
#
# Production is only read (pg_dump). The copy is restored into a scratch
# database first and swapped in for the local one only once the restore has
# fully succeeded, so a failure leaves the local database as it was. The local
# database is backed up to tmp/db-backups/ before it is replaced.
#
# Only the public schema is copied, like db-copy-local-to-prod.sh. The
# extensions production uses are created locally before the restore.
#
#   scripts/db-copy-prod-to-local.sh                   # copy prod -> local
#   scripts/db-copy-prod-to-local.sh --restore FILE    # put a dump back into local
#
# LOCAL_DATABASE_URL defaults to the local docker database; PROD_DATABASE_URL
# defaults to DATABASE_URL in .env.production. Your IP must be allowed through
# the production Postgres firewall.
set -euo pipefail

cd "$(dirname "$0")/.."

LOCAL_URL="${LOCAL_DATABASE_URL:-postgresql://postgres:postgres@localhost:5434/find_me?sslmode=disable}"
PROD_URL="${PROD_DATABASE_URL:-$(sed -n 's/^DATABASE_URL=//p' .env.production 2>/dev/null | tail -1)}"
BACKUP_DIR="tmp/db-backups"
# Not-yet-published production events: copying them would make the local API publish them.
EXCLUDE_TABLE_DATA=(outbox_event)
SUMMARY_TABLES=(users retailers store product product_variant inventory catalog_item)

die() {
	echo "❌ $*" >&2
	exit 1
}

q() {
	psql "$1" -X -q -v ON_ERROR_STOP=1 -tA -c "$2"
}

# with_database swaps the database name in a connection URL.
with_database() {
	printf '%s' "$1" | sed -E "s#/([^/?]+)(\\?|$)#/$2\\2#"
}

describe() {
	q "$1" "SELECT COALESCE(inet_server_addr()::text, 'local socket') || ':' || inet_server_port() || '/' || current_database()"
}

migration_version() {
	q "$1" "SELECT version || CASE WHEN dirty THEN ' (dirty)' ELSE '' END FROM schema_migrations" 2>/dev/null || echo "none"
}

# row_counts checks each table exists first: a fresh local database may have none of them.
row_counts() {
	local url=$1 t
	for t in "${SUMMARY_TABLES[@]}"; do
		if [ "$(q "$url" "SELECT to_regclass('public.$t') IS NOT NULL")" = t ]; then
			echo "$t $(q "$url" "SELECT count(*) FROM public.$t")"
		else
			echo "$t -"
		fi
	done
}

# restorable_toc lists a dump's entries minus the public schema itself, which
# a new database already has.
restorable_toc() {
	pg_restore -l "$1" | grep -Ev ' (SCHEMA - public|COMMENT - SCHEMA public) '
}

restore_file=""
case "${1:-}" in
"") ;;
--restore)
	restore_file="${2:-}"
	[ -f "$restore_file" ] || die "usage: $0 --restore FILE (no such file: ${restore_file:-<none>})"
	;;
*) die "usage: $0 [--restore FILE]" ;;
esac

for tool in pg_dump pg_restore psql; do
	command -v "$tool" >/dev/null || die "$tool not found (brew install libpq)"
done

# Everything below drops and recreates the target, so it must never be a remote server.
[[ "$LOCAL_URL" =~ @(localhost|127\.0\.0\.1|\[::1\])[:/] ]] ||
	die "LOCAL_DATABASE_URL must point at localhost; refusing to replace ${LOCAL_URL#*@}"
q "$LOCAL_URL" 'SELECT 1' >/dev/null || die "cannot connect to the local database (is docker running? try: make docker-up)"
local_db=$(q "$LOCAL_URL" 'SELECT current_database()')
staging_db="${local_db}_incoming"
[[ "$local_db" =~ ^[a-z_][a-z0-9_]*$ ]] || die "unexpected local database name: $local_db"
admin_url=$(with_database "$LOCAL_URL" postgres)
staging_url=$(with_database "$LOCAL_URL" "$staging_db")
q "$admin_url" 'SELECT 1' >/dev/null || die "cannot connect to the local postgres maintenance database"
target=$(describe "$LOCAL_URL")

echo ""
if [ -z "$restore_file" ]; then
	[ -n "$PROD_URL" ] || die "PROD_DATABASE_URL is not set and .env.production has no DATABASE_URL"
	q "$PROD_URL" 'SELECT 1' >/dev/null ||
		die "cannot connect to production (is your IP allowed through the Postgres firewall?)"
	source=$(describe "$PROD_URL")
	[ "$source" != "$target" ] || die "production and local are the same database ($target)"
	extensions=$(q "$PROD_URL" "SELECT extname FROM pg_extension WHERE extname <> 'plpgsql' ORDER BY 1")

	echo "From (PROD):   $source   migration $(migration_version "$PROD_URL")"
	echo "To   (local):  $target   migration $(migration_version "$LOCAL_URL")"
	echo ""
	echo "Rows            prod  ->  local now"
	paste <(row_counts "$PROD_URL") <(row_counts "$LOCAL_URL" | cut -d' ' -f2) |
		awk '{ printf "  %-15s %6s  ->  %s\n", $1, $2, $3 }'
else
	extensions="postgis pg_trgm"
	echo "Restoring $restore_file"
	echo "To   (local):  $target"
fi

echo ""
echo "⚠️  The local database '$local_db' will be DROPPED and replaced (a backup is taken first)."
echo "   Anything connected to it, like a running API, is disconnected."
read -r -p "Type the local database name ($local_db) to continue: " answer
[ "$answer" = "$local_db" ] || die "cancelled"

mkdir -p "$BACKUP_DIR"
stamp=$(date +%Y%m%d-%H%M%S)
backup="$BACKUP_DIR/local-$stamp.dump"
echo ""
echo "→ Backing up the local database to $backup"
pg_dump "$LOCAL_URL" --format=custom --no-owner --no-privileges --schema=public --file="$backup"

dump=$restore_file
if [ -z "$dump" ]; then
	dump="$BACKUP_DIR/prod-$stamp.dump"
	exclude=()
	for t in "${EXCLUDE_TABLE_DATA[@]}"; do
		exclude+=("--exclude-table-data=public.$t")
	done
	echo "→ Dumping production to $dump (read-only)"
	pg_dump "$PROD_URL" --format=custom --no-owner --no-privileges --schema=public "${exclude[@]}" --file="$dump"
fi

echo "→ Restoring into a scratch database ($staging_db)"
psql "$admin_url" -X -q -v ON_ERROR_STOP=1 -c 'SET client_min_messages = warning' -c "DROP DATABASE IF EXISTS \"$staging_db\" WITH (FORCE)"
q "$admin_url" "CREATE DATABASE \"$staging_db\"" >/dev/null
toc="$dump.list"
cleanup_staging() {
	rm -f "$toc"
	q "$admin_url" "DROP DATABASE IF EXISTS \"$staging_db\" WITH (FORCE)" >/dev/null 2>&1 || true
}
trap cleanup_staging EXIT

for ext in $extensions; do
	if [ "$(q "$staging_url" "SELECT count(*) FROM pg_available_extensions WHERE name = '$ext'")" = 1 ]; then
		q "$staging_url" "CREATE EXTENSION IF NOT EXISTS \"$ext\"" >/dev/null
	else
		echo "   ⚠️  production extension $ext isn't available locally; skipped"
	fi
done

restorable_toc "$dump" >"$toc"
# Restored through psql because pg_restore 17+ emits SET transaction_timeout,
# which older servers reject.
pg_restore --no-owner --no-privileges --use-list="$toc" --file=- "$dump" |
	grep -v '^SET transaction_timeout' |
	psql "$staging_url" -X -q -v ON_ERROR_STOP=1 --single-transaction >/dev/null
rm -f "$toc"
q "$staging_url" 'ANALYZE' >/dev/null

echo "→ Swapping it in for $local_db"
q "$admin_url" "DROP DATABASE \"$local_db\" WITH (FORCE)" >/dev/null
q "$admin_url" "ALTER DATABASE \"$staging_db\" RENAME TO \"$local_db\"" >/dev/null
trap - EXIT

echo ""
echo "✅ The local database now matches ${restore_file:-production}."
row_counts "$LOCAL_URL" | awk '{ printf "  %-15s %6s\n", $1, $2 }'

copied=$(migration_version "$LOCAL_URL")
latest=$(find migrations -name '*.up.sql' -exec basename {} \; | sed -E 's/^0*([0-9]+)_.*/\1/' | sort -n | tail -1)
if [ -n "$latest" ] && [ "${copied%% *}" != "$latest" ]; then
	echo ""
	echo "ℹ️  The copy is at migration $copied; this branch has up to $latest. Run: make migrate-up (choose DEV)"
fi
echo ""
echo "To undo:  make db-copy-prod-to-local RESTORE=$backup"
