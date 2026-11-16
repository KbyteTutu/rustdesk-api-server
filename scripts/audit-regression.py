"""Run local audit regression checks and preserve evidence, including failures.

Requires Go, Node/npm, Docker and the existing local Rust audit toolchain image.
Does not deploy services. The isolated smoke test cleans its own temporary data.
Output defaults to a new private temporary directory (override with --output).
"""
import argparse
import datetime
import json
import os
import shutil
import subprocess
import tempfile
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output')
    parser.add_argument('--rust-cache', default=str(Path.home() / '.cache/rustdesk-audit-cargo'))
    args = parser.parse_args()
    output = Path(args.output or tempfile.mkdtemp(prefix='rustdesk-audit-regression-')).resolve()
    output.mkdir(parents=True, exist_ok=True, mode=0o700)
    env = os.environ.copy()
    node_dir = Path.home() / '.local/share/pi-node/node-v22.23.1-linux-x64/bin'
    env['PATH'] = str(node_dir) + os.pathsep + env.get('PATH', '')
    env.setdefault('RUSTDESK_SMOKE_IMAGE', 'rustdesk-audit-toolchain:20261114')
    go = shutil.which('go', path=env['PATH']) or 'go'
    jobs = [
        ('go-race', [go, 'test', '-mod=readonly', '-count=1', '-race', '-json', './...'], REPO / 'api', 240),
        ('go-vet', [go, 'vet', '-mod=readonly', './...'], REPO / 'api', 120),
        ('go-build', [go, 'build', '-mod=readonly', './...'], REPO / 'api', 120),
        ('web-auth', ['node', '--test', 'web/tests/audit-auth.test.mjs'], REPO, 30),
        ('web-install', ['npm', 'ci', '--no-fund'], REPO / 'web', 180),
        ('web-build', ['npm', 'run', 'build'], REPO / 'web', 120),
        ('web-audit', ['npm', 'audit', '--audit-level=high', '--json'], REPO / 'web', 90),
        ('rust-tests', ['docker', 'run', '--rm', '--pull=never',
          '-v', f'{REPO}:/src', '-v', f'{Path(args.rust_cache).resolve()}:/cargo', '-w', '/src',
          '-e', 'CARGO_HOME=/cargo', '-e', 'DATABASE_URL=sqlite:///src/db_v2.sqlite3',
          'rustdesk-audit-toolchain:20261114', 'sh', '-c',
          'export PATH=/usr/local/cargo/bin:$PATH; cargo test --locked'], REPO, 300),
        ('isolated-smoke', ['python3', 'scripts/audit-smoke.py'], REPO, 120),
        ('diff-check', ['git', 'diff', '--check'], REPO, 30),
    ]
    results = []
    for name, command, cwd, timeout in jobs:
        started = datetime.datetime.now(datetime.UTC).isoformat()
        logfile = output / (name + '.log')
        try:
            with logfile.open('w') as log:
                result = subprocess.run(command, cwd=cwd, env=env, stdout=log,
                                        stderr=subprocess.STDOUT, timeout=timeout, check=False)
            status, code = ('PASS' if result.returncode == 0 else 'FAIL'), result.returncode
        except (OSError, subprocess.TimeoutExpired) as error:
            status, code = 'BLOCKED', None
            with logfile.open('a') as log:
                log.write('\n' + str(error))
        results.append({'check': name, 'status': status, 'exit': code,
                        'started_utc': started, 'command': command, 'log': str(logfile)})
        print(f'{name}: {status}', flush=True)
        (output / 'results.json').write_text(json.dumps(results, ensure_ascii=False, indent=2))
    # Report each test separately, not just package/build success.
    cases = []
    for line in (output / 'go-race.log').read_text().splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if event.get('Test') and event.get('Action') in ('pass', 'fail', 'skip'):
            cases.append({'package': event['Package'], 'test': event['Test'],
                          'status': event['Action'].upper(), 'seconds': event.get('Elapsed', 0)})
    (output / 'go-cases.json').write_text(json.dumps(cases, ensure_ascii=False, indent=2))
    print(f'{len(cases)} named Go test results; evidence: {output}')
    return 1 if any(row['status'] != 'PASS' for row in results) else 0


if __name__ == '__main__':
    raise SystemExit(main())
