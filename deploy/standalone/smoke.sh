#!/bin/sh
set -eu

image=${1:?Pass a locally built Drive image tag}
here=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
work=$(mktemp -d)
export COMPOSE_PROJECT_NAME="drive-install-test-$$"
export DRIVE_IMAGE="$image"
export DRIVE_BIND=127.0.0.1:0

cd "$work"
cp "$here/compose.yaml" "$here/prepare.sh" .
trap 'docker compose down -v >/dev/null 2>&1 || true; cd /; rm -r -- "$work"' EXIT
cat > fixture.toml <<'EOF'
[profiles.main]
cookie = "UID=1_A1; CID=fixture; SEID=fixture"
EOF
sh prepare.sh https://drive.example.test https://pan.example.test fixture.toml
sh prepare.sh https://drive.example.test https://pan.example.test fixture.toml
docker compose up -d --wait --wait-timeout 75

verify() {
    address=$(docker compose port drive 8098)
    python3 - "$address" "$1" <<'PY'
from http.client import HTTPConnection
from http.cookies import SimpleCookie
from pathlib import Path
import json
import sys

address, password_path = sys.argv[1:]
password = Path(password_path).read_text().strip()

def call(method, url, expected, payload=None, cookie=""):
    headers = {"Origin": "https://drive.example.test"}
    if payload is not None:
        payload = json.dumps(payload)
        headers["Content-Type"] = "application/json"
    if cookie:
        headers["Cookie"] = cookie
    connection = HTTPConnection(address, timeout=10)
    try:
        connection.request(method, url, payload, headers)
        response = connection.getresponse()
        body = response.read()
        assert response.status == expected, (method, url, response.status)
        return response.getheader("Set-Cookie"), body
    finally:
        connection.close()

call("GET", "/healthz", 200)
call("GET", "/", 200)
call("GET", "/api/session", 401)
raw, _ = call("POST", "/api/login", 204, {"login": "Admin", "password": password})
jar = SimpleCookie(raw)
cookie = "__Host-drive=" + jar["__Host-drive"].value
_, body = call("GET", "/api/session", 200, cookie=cookie)
account = json.loads(body)
assert account["role"] == "admin" and account["setup"] is True
_, body = call("GET", "/api/shares/mine", 200, cookie=cookie)
assert json.loads(body) == {"items": [], "more": False}
print("Fresh stack ready: HTTPS-origin login, first-run OpenList tab, and personal shares verified")
PY
}

verify private/admin-password
printf '%s\n' 'FixtureChanged42!' > private/changed-password
docker compose exec -T drive /usr/local/bin/drive replace-admin admin admin /run/drive/changed-password </dev/null
docker compose restart drive
docker compose up -d --wait --wait-timeout 75
verify private/changed-password
printf '%s\n' 'Restart preserved the updated administrator password.'
