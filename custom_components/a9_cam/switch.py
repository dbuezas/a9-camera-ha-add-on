"""Camera settings that are on or off."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from homeassistant.components.switch import SwitchEntity, SwitchEntityDescription
from homeassistant.const import EntityCategory
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from . import A9ConfigEntry, A9Hub
from .entity import A9Entity, setup_platform_entities


@dataclass(frozen=True, kw_only=True)
class A9SwitchDescription(SwitchEntityDescription):
    code: int  # command code
    field: str  # base info field, also the command field
    on: int = 1
    off: int = 0
    # another command with the same values, for other firmware versions
    alt_code: int | None = None
    alt_field: str | None = None


SWITCHES = [
    A9SwitchDescription(key="infrared", translation_key="infrared", code=202, field="IrLed"),
    A9SwitchDescription(
        # newer firmware ignores instLed and uses ledEI, older firmware ignores ledEI
        key="status_led", translation_key="status_led", code=210, field="instLed",
        alt_code=220, alt_field="ledEI", entity_category=EntityCategory.CONFIG,
    ),
    # the app only uses 0 (normal) and 4 (upside down)
    A9SwitchDescription(
        key="flip", translation_key="flip", code=216, field="mirrorFlip", on=4,
        entity_category=EntityCategory.CONFIG,
    ),
]


async def async_setup_entry(
    hass: HomeAssistant, entry: A9ConfigEntry, async_add_entities: AddEntitiesCallback
) -> None:
    setup_platform_entities(
        hass,
        entry,
        async_add_entities,
        lambda hub, uid: [*(A9Switch(hub, uid, d) for d in SWITCHES), A9Audio(hub, uid)],
    )


class A9Switch(A9Entity, SwitchEntity):
    entity_description: A9SwitchDescription

    def __init__(self, hub: A9Hub, uid: str, description: A9SwitchDescription) -> None:
        super().__init__(hub, uid, description.key)
        self.entity_description = description

    @property
    def is_on(self) -> bool | None:
        d = self.entity_description
        value = self.info.get(d.field)
        if value is None and d.alt_field:
            value = self.info.get(d.alt_field)
        if value is None:
            return None
        return value != self.entity_description.off

    async def async_turn_on(self, **kwargs: Any) -> None:
        await self._set(self.entity_description.on)

    async def async_turn_off(self, **kwargs: Any) -> None:
        await self._set(self.entity_description.off)

    async def _set(self, value: int) -> None:
        d = self.entity_description
        await self.send({"code": d.code, d.field: value})
        if d.alt_code is not None:
            await self.send({"code": d.alt_code, d.alt_field: value})


class A9Audio(A9Entity, SwitchEntity):
    """Kept by the add-on: off asks the camera for video only."""

    _attr_translation_key = "audio"
    _attr_entity_category = EntityCategory.CONFIG

    def __init__(self, hub: A9Hub, uid: str) -> None:
        super().__init__(hub, uid, "audio")

    @property
    def is_on(self) -> bool | None:
        return self.state_data.get("audio")

    async def async_turn_on(self, **kwargs: Any) -> None:
        await self.hub.client.set_audio(self.uid, True)

    async def async_turn_off(self, **kwargs: Any) -> None:
        await self.hub.client.set_audio(self.uid, False)
