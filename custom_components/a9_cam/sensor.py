"""Battery, WiFi network and SD card."""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

from homeassistant.components.sensor import (
    SensorDeviceClass,
    SensorEntity,
    SensorEntityDescription,
    SensorStateClass,
)
from homeassistant.const import PERCENTAGE, EntityCategory
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from . import A9ConfigEntry, A9Hub
from .entity import A9Entity, setup_platform_entities

SD_STATES = {1: "ok", 0: "none", -1: "error", -2: "unsupported", -3: "no_file", -4: "corrupted"}


def _battery(info: dict[str, Any]) -> Any:
    value = info.get("devPower")
    return None if value is None or value < 0 else value  # -1: no battery


@dataclass(frozen=True, kw_only=True)
class A9SensorDescription(SensorEntityDescription):
    value: Callable[[dict[str, Any]], Any]


SENSORS = [
    A9SensorDescription(
        key="battery",
        device_class=SensorDeviceClass.BATTERY,
        state_class=SensorStateClass.MEASUREMENT,
        native_unit_of_measurement=PERCENTAGE,
        value=_battery,
    ),
    A9SensorDescription(
        key="wifi",
        translation_key="wifi",
        entity_category=EntityCategory.DIAGNOSTIC,
        value=lambda info: info.get("wifiName"),
    ),
    A9SensorDescription(
        key="sd_card",
        translation_key="sd_card",
        device_class=SensorDeviceClass.ENUM,
        options=list(SD_STATES.values()),
        entity_category=EntityCategory.DIAGNOSTIC,
        value=lambda info: SD_STATES.get(info.get("sdDevStatus")),
    ),
]


async def async_setup_entry(
    hass: HomeAssistant, entry: A9ConfigEntry, async_add_entities: AddEntitiesCallback
) -> None:
    setup_platform_entities(
        hass, entry, async_add_entities, lambda hub, uid: [A9Sensor(hub, uid, d) for d in SENSORS]
    )


class A9Sensor(A9Entity, SensorEntity):
    entity_description: A9SensorDescription

    def __init__(self, hub: A9Hub, uid: str, description: A9SensorDescription) -> None:
        super().__init__(hub, uid, description.key)
        self.entity_description = description

    @property
    def native_value(self) -> Any:
        return self.entity_description.value(self.info)
