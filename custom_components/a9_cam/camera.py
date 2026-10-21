"""Live MJPEG video and snapshots."""

from __future__ import annotations

from aiohttp import web

from homeassistant.components.camera import Camera
from homeassistant.core import HomeAssistant
from homeassistant.helpers.aiohttp_client import (
    async_aiohttp_proxy_web,
    async_get_clientsession,
)
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from . import A9ConfigEntry, A9Hub
from .api import A9Error
from .entity import A9Entity, setup_platform_entities

# Dashboards ask for a picture every 10 s. Waking the camera that often would drain its battery.
SNAPSHOT_MAX_AGE = 60


async def async_setup_entry(
    hass: HomeAssistant, entry: A9ConfigEntry, async_add_entities: AddEntitiesCallback
) -> None:
    setup_platform_entities(hass, entry, async_add_entities, lambda hub, uid: [A9Camera(hub, uid)])


class A9Camera(A9Entity, Camera):
    """The camera only sends video while somebody watches, so it saves battery."""

    _attr_name = None  # the device name
    _attr_brand = "Naxclow"
    _attr_model = "A9 / V720"

    def __init__(self, hub: A9Hub, uid: str) -> None:
        A9Entity.__init__(self, hub, uid, "camera")
        Camera.__init__(self)
        self._last_image: bytes | None = None

    @property
    def is_streaming(self) -> bool:
        return bool(self.state_data.get("streaming"))

    async def async_camera_image(self, width: int | None = None, height: int | None = None) -> bytes | None:
        try:
            self._last_image = await self.hub.client.snapshot(self.uid, max_age=SNAPSHOT_MAX_AGE)
        except A9Error:
            pass  # keep the last image while the camera is away
        return self._last_image

    async def handle_async_mjpeg_stream(self, request: web.Request) -> web.StreamResponse | None:
        session = async_get_clientsession(self.hass)
        stream = session.get(self.hub.client.mjpeg_url(self.uid))
        return await async_aiohttp_proxy_web(self.hass, request, stream)
