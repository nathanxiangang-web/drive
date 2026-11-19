#!/bin/sh
set -eu

secrets=/run/db-secrets
password=$(cat "$secrets/drive.password")
case "$password" in
  ''|*[!0-9a-f]*) echo "Invalid CloudDrive database password file" >&2; exit 1 ;;
esac
test "${#password}" -ge 32
psql -X -U "$POSTGRES_USER" -d postgres -v ON_ERROR_STOP=1 -q <<SQL
CREATE ROLE cloudsite_drive LOGIN PASSWORD '$password';
CREATE DATABASE cloudsite_drive OWNER cloudsite_drive;
REVOKE CONNECT, TEMP ON DATABASE cloudsite_drive FROM PUBLIC;
SQL
if [ -f "$secrets/drive.dump" ]; then
  pg_restore --exit-on-error --no-owner --no-privileges --role=cloudsite_drive \
    -U "$POSTGRES_USER" -d cloudsite_drive "$secrets/drive.dump"
fi
