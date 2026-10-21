"""Run the real add-on server and a fake camera for the integration tests."""

from __future__ import annotations

from collections.abc import Iterator
from pathlib import Path
import socket
import subprocess
import time

import pytest

SERVER = Path(__file__).parent.parent / "server"
UID = "0800c0000001"


def _free_port() -> int:
    """A port that is free for both TCP and UDP."""
    while True:
        with socket.socket() as s:
            s.bind(("127.0.0.1", 0))
            port = s.getsockname()[1]
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as u:
            try:
                u.bind(("127.0.0.1", port))
            except OSError:
                continue
        return port


@pytest.fixture(autouse=True)
def auto_enable_custom_integrations(enable_custom_integrations):
    yield


@pytest.fixture(scope="session")
def binaries(tmp_path_factory) -> Path:
    out = tmp_path_factory.mktemp("bin")
    subprocess.run(["go", "build", "-o", out / "a9cam", "."], cwd=SERVER, check=True)
    subprocess.run(["go", "build", "-o", out / "fakecam", "./cmd/fakecam"], cwd=SERVER, check=True)
    return out


@pytest.fixture
def addon(binaries: Path, socket_enabled) -> Iterator[int]:
    """Start the server and one fake camera. Yields the HTTP port."""
    http_port, cam_port = _free_port(), _free_port()
    server = subprocess.Popen(
        [binaries / "a9cam", "-http", "", "-ui", f"127.0.0.1:{http_port}", "-port", str(cam_port), "-rtsp", ""],
    )
    deadline = time.time() + 10
    while True:
        try:
            socket.create_connection(("127.0.0.1", http_port), timeout=0.2).close()
            break
        except OSError:
            if time.time() > deadline:
                raise
            time.sleep(0.05)
    cam = subprocess.Popen([binaries / "fakecam", "-server", f"127.0.0.1:{cam_port}", "-uid", UID])
    yield http_port
    cam.terminate()
    server.terminate()
    cam.wait()
    server.wait()
