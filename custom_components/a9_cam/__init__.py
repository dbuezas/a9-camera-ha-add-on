"""A9 / V720 mini cameras, through the A9 Camera add-on."""

from __future__ import annotations

import asyncio
import logging
from typing import Any

from homeassistant.config_entries import ConfigEntry
from homeassistant.const import CONF_HOST, CONF_PORT, Platform
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import ConfigEntryNotReady
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from homeassistant.helpers.dispatcher import async_dispatcher_send

from .api import A9Client, A9Error
from .const import SIGNAL_DEVICE, SIGNAL_NEW_DEVICE

_LOGGER = logging.getLogger(__name__)

PLATFORMS = [
    Platform.BINARY_SENSOR,
    Platform.BUTTON,
    Platform.CAMERA,
    Platform.SELECT,
    Platform.SENSOR,
    Platform.SWITCH,
]

type A9ConfigEntry = ConfigEntry[A9Hub]


class A9Hub:
    """Keeps the state of all cameras, updated live by the add-on."""

    def __init__(self, hass: HomeAssistant, client: A9Client) -> None:
        self.hass = hass
        self.client = client
        self.devices: dict[str, dict[str, Any]] = {}

    def update(self, state: dict[str, Any]) -> None:
        uid = state["uid"]
        new = uid not in self.devices
        self.devices[uid] = state
        if new:
            async_dispatcher_send(self.hass, SIGNAL_NEW_DEVICE, uid)
        else:
            async_dispatcher_send(self.hass, SIGNAL_DEVICE.format(uid))

    def set_all_offline(self) -> None:
        for uid, state in self.devices.items():
            if state.get("online"):
                state["online"] = False
                async_dispatcher_send(self.hass, SIGNAL_DEVICE.format(uid))

    async def listen(self) -> None:
        """Follow the add-on events forever, reconnecting when the add-on restarts."""
        while True:
            try:
                async for state in self.client.events():
                    self.update(state)
            except A9Error as err:
                _LOGGER.debug("Event stream ended: %s", err)
            self.set_all_offline()
            await asyncio.sleep(5)


async def async_setup_entry(hass: HomeAssistant, entry: A9ConfigEntry) -> bool:
    client = A9Client(async_get_clientsession(hass), entry.data[CONF_HOST], entry.data[CONF_PORT])
    try:
        devices = await client.devices()
    except A9Error as err:
        raise ConfigEntryNotReady(f"A9 Camera add-on not reachable: {err}") from err

    hub = A9Hub(hass, client)
    hub.devices = {d["uid"]: d for d in devices}
    entry.runtime_data = hub

    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    entry.async_create_background_task(hass, hub.listen(), "a9_cam events")
    return True


async def async_unload_entry(hass: HomeAssistant, entry: A9ConfigEntry) -> bool:
    return await hass.config_entries.async_unload_platforms(entry, PLATFORMS)
