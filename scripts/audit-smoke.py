"""Isolated smoke test; requires built hbbs/hbbr, Go, Docker and an Alpine image.

Run: python3 scripts/audit-smoke.py
Optional: RUSTDESK_SMOKE_IMAGE (default alpine:3.21, must exist locally).
No published container ports, persistent data, or production credentials are used.
"""

import http.client
import json
import os
import secrets
import shutil
import socket
import stat
import subprocess
import tempfile
import time
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]
IMAGE = os.environ.get("RUSTDESK_SMOKE_IMAGE", "alpine:3.21")
DOCKER = shutil.which("docker")
GO = shutil.which("go")


def main():
    if not DOCKER or not GO:
        raise RuntimeError("Docker and Go must be installed")
    for binary in ("hbbs", "hbbr"):
        if not (REPO / "target/debug" / binary).is_file():
            raise RuntimeError("Build Rust binaries with cargo build --locked first")
    work = Path(tempfile.mkdtemp(prefix="rustdesk-audit-smoke-"))
    name = "rustdesk-audit-smoke-" + secrets.token_hex(5)
    secret = secrets.token_hex(32)
    password = secrets.token_hex(24)  # Exercises native login with >32 bytes.
    process = None
    log = None
    checks = []
    port = 0

    def check(condition, label):
        if not condition:
            raise AssertionError(label)
        checks.append(label)

    def request(path, data=None, token=None, bearer=False):
        headers = {"Content-Type": "application/json"}
        if token:
            headers["Authorization" if bearer else "api-token"] = (
                ("Bearer " if bearer else "") + token
            )
        connection = http.client.HTTPConnection("127.0.0.1", port, timeout=5)
        try:
            connection.request(
                "POST" if data is not None else "GET",
                path,
                body=json.dumps(data) if data is not None else None,
                headers=headers,
            )
            response = connection.getresponse()
            return response.status, json.loads(response.read())
        finally:
            connection.close()

    def admin(path, data=None, token=None):
        status, response = request("/api/admin/" + path, data, token)
        check(status == 200 and response.get("code") == 0, "admin " + path)
        return response["data"]

    def wait_for(predicate, label):
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            try:
                if predicate():
                    return
            except OSError:
                pass
            time.sleep(0.1)
        raise AssertionError("timeout: " + label)

    try:
        subprocess.run(
            [GO, "build", "-mod=readonly", "-o", str(work / "apimain"), "./cmd"],
            cwd=REPO / "api", check=True,
        )
        for directory in ("identity", "api", "public", "hbbs", "hbbr"):
            (work / directory).mkdir(mode=0o700)
        envfile = work / "test.env"
        envfile.write_text(
            "RUSTDESK_API_JWT_KEY=" + secret + "\nMUST_LOGIN=Y\nRUST_LOG=info\n"
        )
        envfile.chmod(0o600)
        shell = """set -eu
cd /smoke/identity
CONTROL_SOCKET=/smoke/hbbs/private/control.sock /bin/hbbs -r 127.0.0.1:21117 -k _ > /smoke/hbbs.log 2>&1 &
p1=$!
CONTROL_SOCKET=/smoke/hbbr/private/control.sock /bin/hbbr > /smoke/hbbr.log 2>&1 &
p2=$!
trap 'kill $p1 $p2 2>/dev/null || true' EXIT INT TERM
wait $p1 $p2
"""
        subprocess.run(
            [DOCKER, "run", "-d", "--pull=never", "--name", name,
             "--network", "none", "--user", f"{os.getuid()}:{os.getgid()}",
             "--env-file", str(envfile), "-v", f"{work}:/smoke",
             "-v", f"{REPO}/target/debug/hbbs:/bin/hbbs:ro",
             "-v", f"{REPO}/target/debug/hbbr:/bin/hbbr:ro",
             IMAGE, "sh", "-c", shell], check=True, stdout=subprocess.DEVNULL,
        )
        wait_for(
            lambda: all((work / role / "private/control.sock").exists()
                        for role in ("hbbs", "hbbr")), "control listeners",
        )
        wait_for(lambda: (work / "identity/id_ed25519.pub").exists(), "public identity")
        check(stat.S_IMODE((work / "identity/id_ed25519").stat().st_mode) == 0o600,
              "private key mode 0600")
        for role in ("hbbs", "hbbr"):
            check(stat.S_IMODE((work / role / "private").stat().st_mode) == 0o700,
                  role + " socket dir mode 0700")
            check(stat.S_IMODE((work / role / "private/control.sock").stat().st_mode) == 0o600,
                  role + " socket mode 0600")
        shutil.copy(work / "identity/id_ed25519.pub", work / "public/id_ed25519.pub")
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        config = (REPO / "api/conf/config.yaml").read_text()
        config = config.replace("0.0.0.0:21114", f"127.0.0.1:{port}")
        config = config.replace("'resources'", json.dumps(str(REPO / "api/resources")))
        config = config.replace("/data/id_ed25519.pub", str(work / "public/id_ed25519.pub"))
        (work / "api/config.yaml").write_text(config)
        env = {key: value for key, value in os.environ.items()
               if not key.startswith("RUSTDESK_API_")}
        env.update(
            RUSTDESK_API_JWT_KEY=secret, RUSTDESK_API_ADMIN_PASSWORD=password,
            RUSTDESK_API_ADMIN_ID_SERVER_SOCKET=str(work / "hbbs/private/control.sock"),
            RUSTDESK_API_ADMIN_RELAY_SERVER_SOCKET=str(work / "hbbr/private/control.sock"),
        )
        log = (work / "api.log").open("w")
        process = subprocess.Popen(
            [str(work / "apimain"), "-c", "config.yaml"], cwd=work / "api",
            env=env, stdout=log, stderr=subprocess.STDOUT,
        )
        wait_for(lambda: request("/api/login-options")[0] == 200, "API startup")
        check(process.poll() is None, "empty data startup")
        token = admin("login", {"username": "admin", "password": password})["token"]
        check(len(token.split(".")) == 3, "JWT login")
        admin("user/current", token=token)
        command = {"target": "21115", "cmd": "ml"}
        check(request("/api/admin/rustdesk/sendCmd", command)[0] == 401,
              "anonymous control denied")
        relay_command = {"target": "21115", "cmd": "rs"}
        before = admin("rustdesk/sendCmd", relay_command, token)
        check(admin("rustdesk/sendCmd", command, token).strip() == "MUST_LOGIN: true",
              "MUST_LOGIN enabled")
        admin("rustdesk/sendCmd", {"target": "21115", "cmd": "aur", "option": "Y"}, token)
        check(admin("rustdesk/sendCmd", relay_command, token) == before,
              "aur does not alter relay list")
        check(bool(admin("rustdesk/sendCmd", {"target": "21117", "cmd": "h"}, token)),
              "hbbr control response")
        admin("user/create", {
            "username": "smoke-user", "nickname": "smoke", "group_id": 1,
            "status": 1, "is_admin": False, "password": password,
        }, token)
        users = admin("user/list?username=smoke-user", token=token)["list"]
        uid = next(user["id"] for user in users if user["username"] == "smoke-user")
        admin("user/changePwd", {"id": uid, "password": password}, token)
        normal = admin("login", {"username": "smoke-user", "password": password})["token"]
        check(request("/api/admin/rustdesk/sendCmd", command, normal)[0] == 403,
              "ordinary user control denied")
        status, native = request("/api/login", {
            "username": "smoke-user", "password": password, "id": "audit-device",
            "uuid": "audit-uuid", "deviceInfo": {"os": "linux", "type": "rustdesk"},
        })
        check(status == 200 and "access_token" in native, "native login with 48-byte password")
        check(request("/api/heartbeat", {"id": "audit-device", "uuid": "audit-uuid"})[0] == 401,
              "anonymous telemetry denied")
        check(request("/api/heartbeat", {"id": "other-device", "uuid": "audit-uuid"},
                      native["access_token"], True)[0] == 403, "mismatched telemetry denied")
        admin("logout", {}, token)
        check(request("/api/admin/user/current", token=token)[0] == 401,
              "logout revokes API session")
        logs = "\n".join((work / name).read_text() for name in ("api.log", "hbbs.log", "hbbr.log"))
        check(all(value not in logs for value in (secret, password, token)),
              "no JWT secret/password/token in startup and request logs")
        print(json.dumps({"result": "PASS", "checks": checks, "count": len(checks)}, indent=2))
    except Exception:
        # Keep sensitive startup/request logs private and delete them during cleanup.
        print(json.dumps({"result": "FAIL", "passed": checks}, indent=2))
        raise
    finally:
        if process is not None:
            process.terminate()
            try:
                process.wait(timeout=12)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
        if log is not None:
            log.close()
        subprocess.run([DOCKER, "rm", "-f", name], stdout=subprocess.DEVNULL,
                       stderr=subprocess.DEVNULL, check=False)
        shutil.rmtree(work)


if __name__ == "__main__":
    main()
