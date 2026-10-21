"""Camera settings with a few choices."""

from __future__ import annotations

from homeassistant.components.select import SelectEntity
from homeassistant.const import EntityCategory
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from . import A9ConfigEntry, A9Hub
from .entity import A9Entity, setup_platform_entities

# The app calls it VGA / HD / FHD, but the camera keeps 640x480 and changes the JPEG quality
QUALITY = {"low": 0, "medium": 1, "high": 2}


async def async_setup_entry(
    hass: HomeAssistant, entry: A9ConfigEntry, async_add_entities: AddEntitiesCallback
) -> None:
    setup_platform_entities(hass, entry, async_add_entities, lambda hub, uid: [A9Quality(hub, uid)])


class A9Quality(A9Entity, SelectEntity):
    _attr_translation_key = "image_quality"
    _attr_entity_category = EntityCategory.CONFIG
    _attr_options = list(QUALITY)

    def __init__(self, hub: A9Hub, uid: str) -> None:
        super().__init__(hub, uid, "image_quality")

    @property
    def current_option(self) -> str | None:
        value = self.info.get("speedGrade")
        return next((k for k, v in QUALITY.items() if v == value), None)

    async def async_select_option(self, option: str) -> None:
        await self.send({"code": 201, "speedGrade": QUALITY[option]})
