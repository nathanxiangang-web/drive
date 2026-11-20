#!/bin/sh
set -eu

usage() {
    printf '%s\n' 'Usage: sh prepare.sh https://drive.example.com https://pan.example.com /path/to/115driver/config.toml' >&2
    exit 2
}

[ "$#" -eq 3 ] || usage
origin=$1
openlist=$2
case "$origin" in https://?*) ;; *) usage ;; esac
case "$openlist" in http://?*|https://?*) ;; *) usage ;; esac
for address in "$origin" "$openlist"; do
    [ "$address" = "$(printf '%s' "$address" | tr -d '\r\n')" ] || usage
    if printf '%s' "$address" | grep -q "[[:space:]']"; then usage; fi
done
case "$3" in /*) provider_config=$3 ;; *) provider_config="$(pwd)/$3" ;; esac
[ -s "$provider_config" ] || { printf '%s\n' '115driver config.toml is missing or empty.' >&2; exit 1; }
command -v openssl >/dev/null || { printf '%s\n' 'openssl is required to initialize the installation.' >&2; exit 1; }

cd "$(dirname "$0")"
if [ -e .env ]; then
    printf '%s\n' 'Already prepared. Edit .env for settings, then run docker compose up -d --wait.'
    exit 0
fi

umask 077
mkdir -p private
lock=private/.prepare
mkdir "$lock"
trap 'rm -f -- "$lock/new"; rmdir -- "$lock"' EXIT
generate() {
    [ -s "$1" ] && return
    openssl rand -hex "$2" > "$lock/new"
    mv -- "$lock/new" "$1"
}
generate private/postgres-password 24
generate private/admin-password 12
generate private/openlist-key 32
[ -s private/115.toml ] || install -m 0600 "$provider_config" private/115.toml
if [ ! -s private/database-url ]; then
    printf 'postgres://drive:%s@postgres:5432/drive?sslmode=disable\n' "$(cat private/postgres-password)" > "$lock/new"
    mv -- "$lock/new" private/database-url
fi

uid=$(id -u)
gid=$(id -g)
if [ "$uid" = 0 ]; then
    uid=10002
    gid=10002
    chown "$uid:$gid" private private/postgres-password private/admin-password private/openlist-key private/115.toml private/database-url
fi
cat > "$lock/new" <<EOF
DRIVE_ORIGIN='$origin'
DRIVE_OPENLIST_BASE='$openlist'
DRIVE_UID=$uid
DRIVE_GID=$gid
EOF
mv -- "$lock/new" .env
printf '%s\n' 'Prepared. First login: admin. The initial password is in private/admin-password.'
printf '%s\n' 'Start with: docker compose up -d --wait'
