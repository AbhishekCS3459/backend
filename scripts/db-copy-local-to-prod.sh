#!/usr/bin/env bash
# Replaces the production database's app data with a copy of the local one.
#
# Everything in production's public schema (tables, rows, sequences, indexes)
# is dropped and recreated from the local database. Production is backed up to
# tmp/db-backups/ first, and the restore runs in one transaction: if anything
# fails, production is left exactly as it was.
#
# Only the public schema is copied. Extensions are not touched (production
# must already have postgis and pg_trgm, which the migrations create), so the
# extra schemas the local PostGIS image adds (tiger, topology) stay local.
#
#   scripts/db-copy-local-to-prod.sh                   # copy local -> prod
#   scripts/db-copy-local-to-prod.sh --restore FILE    # put a backup back into prod
#
# SOURCE_DATABASE_URL defaults to the local docker database; PROD_DATABASE_URL
# defaults to DATABASE_URL in .env.production. Your IP must be allowed through
# the production Postgres firewall.
set -euo pipefail

cd "$(dirname "$0")/.."

SOURCE_URL="${SOURCE_DATABASE_URL:-postgresql://postgres:postgres@localhost:5434/find_me?sslmode=disable}"
TARGET_URL="${PROD_DATABASE_URL:-$(sed -n 's/^DATABASE_URL=//p' .env.production 2>/dev/null | tail -1)}"
BACKUP_DIR="tmp/db-backups"
REQUIRED_EXTENSIONS=(postgis pg_trgm)
# Not-yet-published events: copying them would make production publish local events.
EXCLUDE_TABLE_DATA=(outbox_event)
SUMMARY_TABLES=(users retailers store product product_variant inventory catalog_item)

die() {
	echo "❌ $*" >&2
	exit 1
}

q() {
	psql "$1" -X -q -v ON_ERROR_STOP=1 -tA -c "$2"
}

# describe prints host:port/database without credentials.
describe() {
	q "$1" "SELECT COALESCE(inet_server_addr()::text, 'local socket') || ':' || inet_server_port() || '/' || current_database()"
}

migration_version() {
	q "$1" "SELECT version || CASE WHEN dirty THEN ' (dirty)' ELSE '' END FROM schema_migrations" 2>/dev/null || echo "none"
}

row_counts() {
	local url=$1 sql="" t
	for t in "${SUMMARY_TABLES[@]}"; do
		sql+="SELECT '$t', CASE WHEN to_regclass('public.$t') IS NULL THEN '-' ELSE (SELECT count(*) FROM public.$t)::text END UNION ALL "
	done
	q "$url" "${sql% UNION ALL }" | tr '|' ' '
}

# restorable_toc lists a dump's entries minus the public schema itself, which
# production already has and which --clean would otherwise try to drop.
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

[ -n "$TARGET_URL" ] || die "PROD_DATABASE_URL is not set and .env.production has no DATABASE_URL"
for tool in pg_dump pg_restore psql; do
	command -v "$tool" >/dev/null || die "$tool not found (brew install libpq)"
done

q "$TARGET_URL" 'SELECT 1' >/dev/null ||
	die "cannot connect to production (is your IP allowed through the Postgres firewall?)"
target=$(describe "$TARGET_URL")
target_db=$(q "$TARGET_URL" 'SELECT current_database()')

for ext in "${REQUIRED_EXTENSIONS[@]}"; do
	[ "$(q "$TARGET_URL" "SELECT count(*) FROM pg_extension WHERE extname = '$ext'")" = 1 ] ||
		die "production is missing the $ext extension; run the migrations there first"
done

echo ""
if [ -z "$restore_file" ]; then
	q "$SOURCE_URL" 'SELECT 1' >/dev/null || die "cannot connect to the local database (is docker running?)"
	source=$(describe "$SOURCE_URL")
	[ "$source" != "$target" ] || die "source and production are the same database ($target)"

	source_version=$(migration_version "$SOURCE_URL")
	target_version=$(migration_version "$TARGET_URL")
	case "$source_version$target_version" in *dirty*) die "a migration is dirty (local $source_version, prod $target_version)" ;; esac
	[ "$source_version" = "$target_version" ] ||
		die "migration versions differ (local $source_version, prod $target_version); bring the one behind up to date first"

	localhost_images=$(q "$SOURCE_URL" "
		SELECT (SELECT count(*) FROM product_image WHERE image_url ~* '^https?://(localhost|127\.0\.0\.1)')
		     + (SELECT count(*) FROM catalog_item WHERE image_url ~* '^https?://(localhost|127\.0\.0\.1)')")
	if [ "$localhost_images" != 0 ]; then
		echo "⚠️  $localhost_images image URLs point at localhost and will be broken in production."
	fi

	echo "From (local):  $source   migration $source_version"
	echo "To   (PROD):   $target   migration $target_version"
	echo ""
	echo "Rows            local  ->  prod now"
	paste <(row_counts "$SOURCE_URL") <(row_counts "$TARGET_URL" | cut -d' ' -f2) |
		awk '{ printf "  %-15s %6s  ->  %s\n", $1, $2, $3 }'
else
	echo "Restoring $restore_file"
	echo "To   (PROD):   $target"
fi

echo ""
echo "⚠️  Every table in production's public schema will be DROPPED and replaced."
echo "   Production users, stores, products and stock that aren't in the copy are lost."
read -r -p "Type the production database name ($target_db) to continue: " answer
[ "$answer" = "$target_db" ] || die "cancelled"

mkdir -p "$BACKUP_DIR"
stamp=$(date +%Y%m%d-%H%M%S)
backup="$BACKUP_DIR/prod-$stamp.dump"
echo ""
echo "→ Backing up production to $backup"
pg_dump "$TARGET_URL" --format=custom --no-owner --no-privileges --schema=public --file="$backup"

dump=$restore_file
if [ -z "$dump" ]; then
	dump="$BACKUP_DIR/local-$stamp.dump"
	exclude=()
	for t in "${EXCLUDE_TABLE_DATA[@]}"; do
		exclude+=("--exclude-table-data=public.$t")
	done
	echo "→ Dumping local database to $dump"
	pg_dump "$SOURCE_URL" --format=custom --no-owner --no-privileges --schema=public "${exclude[@]}" --file="$dump"
fi

toc="$dump.list"
restorable_toc "$dump" >"$toc"
echo "→ Replacing production data (one transaction)"
# Restored through psql because pg_restore 17+ emits SET transaction_timeout,
# which older servers reject.
pg_restore --clean --if-exists --no-owner --no-privileges --use-list="$toc" --file=- "$dump" |
	grep -v '^SET transaction_timeout' |
	psql "$TARGET_URL" -X -q -v ON_ERROR_STOP=1 --single-transaction >/dev/null
rm -f "$toc"

echo "→ Updating planner statistics"
q "$TARGET_URL" 'ANALYZE' >/dev/null

echo ""
echo "✅ Production now matches ${restore_file:-the local database}."
row_counts "$TARGET_URL" | awk '{ printf "  %-15s %6s\n", $1, $2 }'
echo ""
echo "To undo:  make db-copy-local-to-prod RESTORE=$backup"
