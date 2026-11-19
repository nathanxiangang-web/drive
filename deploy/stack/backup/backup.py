import argparse
import fcntl
import hashlib
import json
import os
import re
import shutil
import signal
import sqlite3
import stat
import subprocess
import tempfile
import time
from datetime import datetime
from pathlib import Path
from urllib.parse import urlsplit
from zoneinfo import ZoneInfo


SOURCE = Path('/source')
STATE = Path('/state')
TZ = ZoneInfo('Asia/Shanghai')
DEST = '/Backup'
ARCHIVE = re.compile(r'my-servers[0-9]{8}\.gz\.tar')
FILES = (
    'compose.yaml', 'postgres-init.sh', 'traefik/routes.yaml', 'acme/acme.json',
    'backup/Dockerfile', 'backup/.dockerignore', 'backup/backup.py',
    'backup/恢复说明.md', 'backup/115driver', 'backup/LICENSE.115driver',
    'drive/revision', 'drive/artifact/Dockerfile', 'drive/artifact/drive',
    'drive/private/database-url', 'drive/private/openlist-key', 'drive/private/openlist-token',
    'private/postgres/postgres.password', 'private/115drive/config.toml',
    'private/openlist/admin.password', 'openlist/data/config.json',
)


class BackupError(Exception):
    pass


def log(message):
    print(f'{datetime.now(TZ).isoformat(timespec="seconds")} {message}', flush=True)


def run(argv, label, *, env=None, timeout=900):
    try:
        result = subprocess.run(argv, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                env=env, timeout=timeout, check=False)
    except (subprocess.TimeoutExpired, OSError) as exc:
        raise BackupError(f'{label}: {type(exc).__name__}') from None
    if result.returncode:
        raise BackupError(f'{label}: exit {result.returncode}')
    return result.stdout


def preserve(source, target):
    metadata = source.stat()
    os.chown(target, metadata.st_uid, metadata.st_gid)
    target.chmod(stat.S_IMODE(metadata.st_mode))


def copy_deployment(tree):
    for name in FILES:
        relative = Path(name)
        source = SOURCE / relative
        if not source.is_file() or source.resolve(strict=True) != source:
            raise BackupError(f'Missing regular deployment file: {name}')
        target = tree / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        for directory in reversed(relative.parents[:-1]):
            preserve(SOURCE / directory, tree / directory)
        shutil.copy2(source, target)
        preserve(source, target)


def pg_snapshot(tree):
    try:
        connection = urlsplit((tree / 'drive/private/database-url').read_text().strip())
        password = connection.password
        port = connection.port or 5432
        if (connection.scheme not in ('postgres', 'postgresql') or connection.hostname != 'postgres'
                or connection.username != 'cloudsite_drive' or connection.path != '/cloudsite_drive'
                or not re.fullmatch(r'[0-9a-f]{32,128}', password or '')):
            raise ValueError
    except (OSError, ValueError):
        raise BackupError('Invalid CloudDrive database configuration') from None
    saved = tree / 'private/postgres'
    password_file = saved / 'drive.password'
    password_file.write_text(password + '\n')
    preserve(saved / 'postgres.password', password_file)
    dump = saved / 'drive.dump'
    env = dict(os.environ, PGHOST=connection.hostname, PGPORT=str(port), PGUSER=connection.username,
               PGDATABASE='cloudsite_drive', PGPASSWORD=password, PGCONNECT_TIMEOUT='10')
    run(['pg_dump', '--format=custom', '--compress=0', '--no-owner', '--no-privileges',
         '--file', str(dump)], 'snapshot CloudDrive PostgreSQL database', env=env)
    preserve(saved / 'postgres.password', dump)
    contents = run(['pg_restore', '--list', str(dump)], 'read logical database backup')
    for table in (b'drive_accounts', b'drive_jobs', b'drive_settings'):
        if b'TABLE DATA public ' + table not in contents:
            raise BackupError('CloudDrive PostgreSQL snapshot is incomplete')
    log('Verified CloudDrive PostgreSQL logical dump')


def sqlite_snapshot(tree):
    source = SOURCE / 'openlist/data/data.db'
    target = tree / 'openlist/data/data.db'
    with sqlite3.connect(f'{source.as_uri()}?mode=ro', uri=True) as origin, sqlite3.connect(target) as copy:
        origin.backup(copy, pages=128, sleep=0.1)
        if copy.execute('PRAGMA quick_check').fetchall() != [('ok',)]:
            raise BackupError('OpenList SQLite integrity check failed')
    preserve(source, target)
    log('Verified OpenList SQLite snapshot')


def create_archive(archive):
    disk = os.statvfs(STATE)
    if disk.f_bavail * disk.f_frsize < 512 * 1024**2:
        raise BackupError('Local spool has less than 512 MiB free')
    with tempfile.TemporaryDirectory(prefix='stage-', dir=STATE) as scratch:
        tree = Path(scratch) / 'my-servers'
        tree.mkdir(mode=0o700)
        copy_deployment(tree)
        pg_snapshot(tree)
        sqlite_snapshot(tree)
        (tree / 'backup/recovered-archive.txt').write_text(archive.name + '\n')
        partial = Path(scratch) / archive.name
        run(['tar', '--numeric-owner', '-czf', str(partial), '-C', scratch, tree.name], 'write tar.gz')
        partial.chmod(0o600)
        run(['gzip', '-t', str(partial)], 'verify tar.gz')
        partial.replace(archive)
    log(f'Created {archive.name}, {archive.stat().st_size} bytes')


def driver(*args):
    argv = ['115driver', '--config', str(SOURCE / 'private/115drive/config.toml'), '--json', *map(str, args)]
    try:
        timeout = 10800 if args[0] in ('upload', 'download') else 120
        result = subprocess.run(argv, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout, check=False)
        response = json.loads(result.stdout)
    except (OSError, subprocess.TimeoutExpired, ValueError):
        raise BackupError(f'115drive {args[0]}: request or response failed') from None
    if not isinstance(response, dict):
        raise BackupError(f'115drive {args[0]}: unexpected response')
    if result.returncode == 0 and response.get('success') is True:
        return response.get('data') or {}
    if args[0] == 'stat' and response.get('code') == 3 and 'directory not found:' in str(response.get('error', '')):
        return None
    code = response.get('code')
    raise BackupError(f'115drive {args[0]}: failed (code {code if isinstance(code, int) else "unknown"})')


def ensure_folder():
    folder = driver('stat', DEST)
    if folder is None:
        try:
            driver('mkdir', DEST)
        except BackupError:
            # A concurrent writer can create the directory after the first stat.
            if driver('stat', DEST) is None:
                raise
        folder = driver('stat', DEST)
        log('115drive Backup directory is available')
    if not folder or folder.get('is_dir') is not True:
        raise BackupError('115drive Backup path is not a directory')


def file_hash(path, algorithm):
    with path.open('rb') as source:
        return hashlib.file_digest(source, algorithm).hexdigest()


def remote_file(name):
    # 115's directory listing includes the file hash. Use pagination and exact
    # matching to avoid relying on the separate file-detail response schema.
    offset = 0
    match = None
    while True:
        page = driver('ls', DEST, '--limit', 100, '--offset', offset)
        if not isinstance(page.get('files'), list):
            raise BackupError('115drive directory listing is incomplete')
        for entry in page['files']:
            if entry.get('name') == name:
                if match is not None:
                    raise BackupError('115drive found duplicate backup names')
                match = entry
        if page.get('has_more') is not True:
            return match
        next_offset = page.get('next_offset')
        if not isinstance(next_offset, int) or next_offset <= offset:
            raise BackupError('115drive directory pagination did not advance')
        offset = next_offset


def upload(archive, *, verify_download):
    ensure_folder()
    remote = f'{DEST}/{archive.name}'
    prior = remote_file(archive.name)
    if prior is None:
        driver('upload', archive, DEST)
        log(f'115drive upload acknowledged for {archive.name}')
    stored = remote_file(archive.name)
    size = archive.stat().st_size
    if not stored or stored.get('is_dir') or int(stored.get('size', -1)) != size:
        raise BackupError('115drive archive name or size failed verification')
    if str(stored.get('sha1')).lower() != file_hash(archive, 'sha1'):
        raise BackupError('115drive archive SHA-1 failed verification')
    digest = file_hash(archive, 'sha256')
    if verify_download:
        with tempfile.TemporaryDirectory(prefix='verify-', dir=STATE) as tmp:
            downloaded = Path(tmp) / archive.name
            driver('download', remote, downloaded)
            if file_hash(downloaded, 'sha256') != digest:
                raise BackupError('115drive copy differs from local backup')
        log(f'Downloaded and verified SHA-256 for {archive.name}')
    receipt = archive.with_name(archive.name + '.sent')
    temp = receipt.with_suffix('.tmp')
    temp.write_text(f'{digest}  {archive.name}\n')
    temp.replace(receipt)
    log(f'115drive verified {remote}, {size} bytes, SHA-256 {digest}')
    # The confirmed offsite copy is durable. Keep the local receipt and retain
    # unconfirmed files until a later attempt succeeds.
    archive.unlink()


def cycle(*, once=False, prepare_only=False, verify_download=False, label=None):
    with (STATE / 'lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        now = datetime.now(TZ)
        suffix = '-' + label if label else ''
        archive = STATE / f'my-servers{now:%Y%m%d}{suffix}.gz.tar'
        receipt = archive.with_name(archive.name + '.sent')
        restored = SOURCE / 'backup/recovered-archive.txt'
        recovered_today = restored.is_file() and restored.read_text().strip() == archive.name
        if (once or now.hour >= 3) and not recovered_today and not receipt.exists() and not archive.exists():
            create_archive(archive)
        if prepare_only:
            return
        if label:
            items = [archive] if archive.is_file() else []
        else:
            items = sorted(STATE.iterdir())
        for item in items:
            if label or ARCHIVE.fullmatch(item.name):
                try:
                    upload(item, verify_download=verify_download)
                except BackupError as error:
                    log(f'{item.name} retained for retry: {error}')
                    if once:
                        raise


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser()
    modes = parser.add_mutually_exclusive_group()
    modes.add_argument('--once', action='store_true')
    modes.add_argument('--prepare-only', action='store_true')
    parser.add_argument('--verify-download', action='store_true')
    parser.add_argument('--label')
    args = parser.parse_args()
    signal.signal(signal.SIGTERM, lambda *_: raise_exit())
    single = args.once or args.prepare_only
    if args.label and (not single or not re.fullmatch(r'[a-z0-9-]{1,32}', args.label)):
        parser.error('--label requires a one-off mode and letters, digits or hyphens')
    if not single:
        log('Daily backup enabled for 03:00 Asia/Shanghai')
    while True:
        try:
            cycle(once=single, prepare_only=args.prepare_only, verify_download=args.verify_download, label=args.label)
        except BackupError as error:
            log(f'Backup failed: {error}')
            if single:
                return 1
        except Exception as error:
            log(f'Backup failed: {type(error).__name__}')
            if single:
                return 1
        if single:
            return 0
        time.sleep(60 - time.time() % 60)


def raise_exit():
    raise SystemExit(0)


if __name__ == '__main__':
    raise SystemExit(main())
