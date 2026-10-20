import argparse
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.request


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def request(base, path, payload=None):
    data = None if payload is None else json.dumps(payload).encode()
    req = urllib.request.Request(base + path, data=data, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=3) as response:
        body = response.read()
        return response.status, json.loads(body) if body else None


def environment(directory, port):
    env = {key: value for key, value in os.environ.items() if key in {"PATH", "HOME", "TMPDIR"}}
    env.update(DATA_DIR=str(directory), LISTEN=f"127.0.0.1:{port}", AGENTFOUNDRY_API_KEY="smoke", ASSISTANT_AGENT_ID="smoke", AGENTFOUNDRY_URL="http://127.0.0.1:1", PROACTIVE_COOLDOWN="1ms")
    return env


def start(binary, directory):
    port = free_port()
    process = subprocess.Popen([binary], env=environment(directory, port), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    base = f"http://127.0.0.1:{port}"
    try:
        for _ in range(50):
            if process.poll() is not None:
                raise RuntimeError("Eve exited before readiness")
            try:
                if request(base, "/readyz")[0] == 200:
                    return process, base
            except OSError:
                time.sleep(0.1)
        raise RuntimeError("Eve did not become ready")
    except BaseException:
        stop(process)
        raise


def stop(process):
    process.terminate()
    try:
        code = process.wait(timeout=15)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait()
        raise RuntimeError("Eve exceeded its shutdown deadline")
    if code != 0:
        raise RuntimeError(f"Eve stopped with exit code {code}")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("binary")
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve())
    with tempfile.TemporaryDirectory(prefix="eve-restore-", dir=os.getenv("EVE_TEST_TMP_DIR")) as temporary:
        source = Path(temporary) / "source"
        process, base = start(binary, source)
        try:
            duplicate = subprocess.run([binary], env=environment(source, free_port()), capture_output=True, timeout=5)
            assert duplicate.returncode != 0 and b"data directory lock" in duplicate.stdout + duplicate.stderr
            _, conversation = request(base, "/api/conversations", {"title": "Backup restore smoke"})
            cid = conversation["id"]
            status, _ = request(base, "/api/email/accounts", {"address": "inbox@example.com", "host": "127.0.0.1", "port": 993, "username": "smoke", "password": "secret", "use_tls": True, "enabled": False})
            assert status == 201
            status, _ = request(base, "/api/notify", {"conversation_id": cid, "content": "Durable notification", "delivery_id": "restore-notification"})
            assert status == 202
            for _ in range(50):
                _, conversation = request(base, f"/api/conversations/{cid}")
                if conversation.get("messages"):
                    break
                time.sleep(0.1)
            assert conversation["messages"][0]["content"] == "Durable notification"
            status, accepted = request(base, f"/api/conversations/{cid}/messages", {"content": "Keep this offline input", "client_id": "restore-input"})
            assert status == 202 and accepted["queued"]
        finally:
            stop(process)
        restored = Path(temporary) / "restored"
        shutil.copytree(source, restored)
        process, base = start(binary, restored)
        try:
            _, conversation = request(base, f"/api/conversations/{cid}")
            assert len(conversation["messages"]) == 2
            assert conversation["messages"][1]["content"] == "Keep this offline input"
            _, accounts = request(base, "/api/email/accounts")
            assert len(accounts) == 1 and accounts[0]["enabled"] is False
            status, duplicate = request(base, f"/api/conversations/{cid}/messages", {"content": "Keep this offline input", "client_id": "restore-input"})
            assert status == 202 and duplicate["message_id"] == accepted["message_id"]
            request(base, "/api/notify", {"conversation_id": cid, "content": "Durable notification", "delivery_id": "restore-notification"})
            _, conversation = request(base, f"/api/conversations/{cid}")
            assert len(conversation["messages"]) == 2
        finally:
            stop(process)
    print("PASS: fresh directory, exclusive lock, offline acceptance, stopped backup/restore, deduplication, and graceful shutdown")


if __name__ == "__main__":
    main()
