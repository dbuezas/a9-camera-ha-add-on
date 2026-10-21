"""Base entity of an A9 camera."""

from __future__ import annotations

from collections.abc import Callable
from typing import Any

from homeassistant.core import HomeAssistant, callback
from homeassistant.helpers import device_registry as dr
from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.dispatcher import async_dispatcher_connect
from homeassistant.helpers.entity import Entity
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from . import A9ConfigEntry, A9Hub
from .const import DOMAIN, SIGNAL_DEVICE, SIGNAL_NEW_DEVICE


class A9Entity(Entity):
    """Entity of one camera. Updates when the add-on reports a change."""

    _attr_has_entity_name = True
    _attr_should_poll = False

    def __init__(self, hub: A9Hub, uid: str, key: str) -> None:
        self.hub = hub
        self.uid = uid
        self._attr_unique_id = f"{uid}_{key}"
        self._attr_device_info = DeviceInfo(
            identifiers={(DOMAIN, uid)},
            name=f"A9 Camera {uid[-4:]}",
            manufacturer="Naxclow",
            model="A9 / V720",
            serial_number=uid,
        )

    @property
    def state_data(self) -> dict[str, Any]:
        return self.hub.devices.get(self.uid, {})

    @property
    def info(self) -> dict[str, Any]:
        return self.state_data.get("info") or {}

    @property
    def available(self) -> bool:
        return bool(self.state_data.get("online"))

    async def async_added_to_hass(self) -> None:
        self.async_on_remove(
            async_dispatcher_connect(self.hass, SIGNAL_DEVICE.format(self.uid), self._handle_update)
        )

    @callback
    def _handle_update(self) -> None:
        self._update_device_registry()
        self.async_write_ha_state()

    @callback
    def _update_device_registry(self) -> None:
        """Keep the firmware version on the device page."""
        version = self.info.get("version")
        if not version:
            return
        device = self.device_entry
        if device and device.sw_version != version:
            dr.async_get(self.hass).async_update_device(device.id, sw_version=str(version))

    async def send(self, content: dict[str, Any]) -> None:
        await self.hub.client.command(self.uid, content)


def setup_platform_entities(
    hass: HomeAssistant,
    entry: A9ConfigEntry,
    async_add_entities: AddEntitiesCallback,
    factory: Callable[[A9Hub, str], list[Entity]],
) -> None:
    """Add entities for the known cameras now and for new cameras later."""
    hub = entry.runtime_data

    @callback
    def add(uid: str) -> None:
        async_add_entities(factory(hub, uid))

    for uid in hub.devices:
        add(uid)
    entry.async_on_unload(async_dispatcher_connect(hass, SIGNAL_NEW_DEVICE, add))
