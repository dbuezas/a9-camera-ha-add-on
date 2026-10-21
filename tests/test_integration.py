"""The integration against the real add-on server and a fake camera."""

from __future__ import annotations

import asyncio

from pytest_homeassistant_custom_component.common import MockConfigEntry

from homeassistant.config_entries import ConfigEntryState
from homeassistant.const import CONF_HOST, CONF_PORT, STATE_OFF, STATE_ON
from homeassistant.core import HomeAssistant
from homeassistant.helpers import device_registry as dr, entity_registry as er

from custom_components.a9_cam.const import DOMAIN

from .conftest import UID


async def _until(hass: HomeAssistant, check, timeout: float = 10) -> None:
    for _ in range(int(timeout / 0.1)):
        if check():
            return
        await asyncio.sleep(0.1)
        await hass.async_block_till_done()
    raise AssertionError("condition not reached")


def _entity(hass: HomeAssistant, platform: str, key: str) -> str:
    entity_id = er.async_get(hass).async_get_entity_id(platform, DOMAIN, f"{UID}_{key}")
    assert entity_id, f"{platform} {key} missing"
    return entity_id


async def _setup(hass: HomeAssistant, port: int) -> MockConfigEntry:
    entry = MockConfigEntry(domain=DOMAIN, data={CONF_HOST: "127.0.0.1", CONF_PORT: port})
    entry.add_to_hass(hass)
    # the camera may take a moment to connect to the fresh server
    for _ in range(50):
        await hass.config_entries.async_setup(entry.entry_id)
        await hass.async_block_till_done()
        if entry.state is ConfigEntryState.LOADED and entry.runtime_data.devices:
            return entry
        await hass.config_entries.async_unload(entry.entry_id)
        await asyncio.sleep(0.1)
    raise AssertionError("camera not found")


async def test_entities_and_switch(hass: HomeAssistant, addon: int) -> None:
    entry = await _setup(hass, addon)

    device = dr.async_get(hass).async_get_device_by_identifier((DOMAIN, UID), entry.entry_id)
    assert device is not None
    assert device.manufacturer == "Naxclow"

    battery = _entity(hass, "sensor", "battery")
    await _until(hass, lambda: hass.states.get(battery).state == "100")
    await _until(hass, lambda: device.id and dr.async_get(hass).async_get(device.id).sw_version == "202302031142")

    ir = _entity(hass, "switch", "infrared")
    assert hass.states.get(ir).state == STATE_OFF
    await hass.services.async_call("switch", "turn_on", {"entity_id": ir}, blocking=True)
    # the camera confirms, the add-on sends an event, the state follows
    await _until(hass, lambda: hass.states.get(ir).state == STATE_ON)

    quality = _entity(hass, "select", "image_quality")
    await hass.services.async_call(
        "select", "select_option", {"entity_id": quality, "option": "high"}, blocking=True
    )
    await _until(hass, lambda: hass.states.get(quality).state == "high")

    audio = _entity(hass, "switch", "audio")
    assert hass.states.get(audio).state == STATE_ON
    await hass.services.async_call("switch", "turn_off", {"entity_id": audio}, blocking=True)
    await _until(hass, lambda: hass.states.get(audio).state == STATE_OFF)

    # sends instLed (210) and ledEI (220), for old and new firmware
    led = _entity(hass, "switch", "status_led")
    assert hass.states.get(led).state == STATE_ON
    await hass.services.async_call("switch", "turn_off", {"entity_id": led}, blocking=True)
    await _until(hass, lambda: hass.states.get(led).state == STATE_OFF)

    assert hass.states.get(_entity(hass, "binary_sensor", "streaming")).state == STATE_OFF
    assert hass.states.get(_entity(hass, "button", "reboot")) is not None

    await hass.config_entries.async_unload(entry.entry_id)


async def test_camera_image(hass: HomeAssistant, addon: int) -> None:
    from homeassistant.components.camera import async_get_image

    entry = await _setup(hass, addon)
    image = await async_get_image(hass, _entity(hass, "camera", "camera"))
    assert image.content_type == "image/jpeg"
    assert image.content[:2] == b"\xff\xd8"
    await hass.config_entries.async_unload(entry.entry_id)


async def test_offline_when_addon_stops(hass: HomeAssistant, addon: int, binaries) -> None:
    entry = await _setup(hass, addon)
    ir = _entity(hass, "switch", "infrared")
    await _until(hass, lambda: hass.states.get(ir).state != "unavailable")

    # stopping the camera makes its entities unavailable
    import subprocess

    subprocess.run(["pkill", "-f", f"fakecam -server 127.0.0.1:.* -uid {UID}"], check=False)
    await _until(hass, lambda: hass.states.get(ir).state == "unavailable")
    await hass.config_entries.async_unload(entry.entry_id)
