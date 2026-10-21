"""Shows when the camera sends video."""

from __future__ import annotations

from homeassistant.components.binary_sensor import BinarySensorDeviceClass, BinarySensorEntity
from homeassistant.const import EntityCategory
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from . import A9ConfigEntry, A9Hub
from .entity import A9Entity, setup_platform_entities


async def async_setup_entry(
    hass: HomeAssistant, entry: A9ConfigEntry, async_add_entities: AddEntitiesCallback
) -> None:
    setup_platform_entities(hass, entry, async_add_entities, lambda hub, uid: [A9Streaming(hub, uid)])


class A9Streaming(A9Entity, BinarySensorEntity):
    _attr_translation_key = "streaming"
    _attr_device_class = BinarySensorDeviceClass.RUNNING
    _attr_entity_category = EntityCategory.DIAGNOSTIC

    def __init__(self, hub: A9Hub, uid: str) -> None:
        super().__init__(hub, uid, "streaming")

    @property
    def is_on(self) -> bool:
        return bool(self.state_data.get("streaming"))
