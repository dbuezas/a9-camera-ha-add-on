"""Reboot the camera."""

from __future__ import annotations

from homeassistant.components.button import ButtonDeviceClass, ButtonEntity
from homeassistant.const import EntityCategory
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from . import A9ConfigEntry, A9Hub
from .entity import A9Entity, setup_platform_entities


async def async_setup_entry(
    hass: HomeAssistant, entry: A9ConfigEntry, async_add_entities: AddEntitiesCallback
) -> None:
    setup_platform_entities(hass, entry, async_add_entities, lambda hub, uid: [A9Reboot(hub, uid)])


class A9Reboot(A9Entity, ButtonEntity):
    _attr_device_class = ButtonDeviceClass.RESTART
    _attr_entity_category = EntityCategory.CONFIG

    def __init__(self, hub: A9Hub, uid: str) -> None:
        super().__init__(hub, uid, "reboot")

    async def async_press(self) -> None:
        await self.send({"code": 299, "reboot": 1})
