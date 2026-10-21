"""Client for the A9 Camera add-on HTTP API."""

from __future__ import annotations

from collections.abc import AsyncIterator
import json
from typing import Any

import aiohttp


class A9Error(Exception):
    """The add-on cannot be reached or refused the request."""


class A9Client:
    """Talks to the add-on: device list, live events, commands, media URLs."""

    def __init__(self, session: aiohttp.ClientSession, host: str, port: int) -> None:
        self._session = session
        self.base = f"http://{host}:{port}"

    async def devices(self) -> list[dict[str, Any]]:
        try:
            async with self._session.get(
                f"{self.base}/api/devices", timeout=aiohttp.ClientTimeout(total=10)
            ) as res:
                res.raise_for_status()
                return await res.json()
        except (aiohttp.ClientError, TimeoutError) as err:
            raise A9Error(str(err)) from err

    async def events(self) -> AsyncIterator[dict[str, Any]]:
        """Yield device states: first all cameras, then every change. Ends on disconnect."""
        timeout = aiohttp.ClientTimeout(total=None, sock_connect=10, sock_read=90)
        try:
            async with self._session.get(f"{self.base}/api/events", timeout=timeout) as res:
                res.raise_for_status()
                async for line in res.content:
                    if line.startswith(b"data: "):
                        yield json.loads(line[6:])
        except (aiohttp.ClientError, TimeoutError) as err:
            raise A9Error(str(err)) from err

    async def command(self, uid: str, content: dict[str, Any]) -> None:
        """Send a command, ex: {"code": 202, "IrLed": 1}. "code" must be the first key."""
        try:
            async with self._session.post(
                f"{self.base}/api/devices/{uid}/command",
                json=content,
                timeout=aiohttp.ClientTimeout(total=10),
            ) as res:
                res.raise_for_status()
        except (aiohttp.ClientError, TimeoutError) as err:
            raise A9Error(str(err)) from err

    async def set_audio(self, uid: str, on: bool) -> None:
        """Audio off asks the camera for video only, so no audio leaves it."""
        try:
            async with self._session.post(
                f"{self.base}/api/devices/{uid}/settings",
                json={"audio": on},
                timeout=aiohttp.ClientTimeout(total=10),
            ) as res:
                res.raise_for_status()
        except (aiohttp.ClientError, TimeoutError) as err:
            raise A9Error(str(err)) from err

    async def snapshot(self, uid: str, max_age: int | None = None) -> bytes:
        """A JPEG frame. With max_age, a frame that recent is reused without waking the camera."""
        params = {"max_age": str(max_age)} if max_age is not None else None
        try:
            async with self._session.get(
                f"{self.base}/dev/{uid}/snapshot", params=params, timeout=aiohttp.ClientTimeout(total=20)
            ) as res:
                res.raise_for_status()
                return await res.read()
        except (aiohttp.ClientError, TimeoutError) as err:
            raise A9Error(str(err)) from err

    def mjpeg_url(self, uid: str) -> str:
        return f"{self.base}/dev/{uid}/live"
