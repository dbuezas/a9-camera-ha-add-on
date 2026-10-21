"""Setup by hand and by add-on discovery."""

from __future__ import annotations

from homeassistant.config_entries import SOURCE_HASSIO, SOURCE_USER
from homeassistant.const import CONF_HOST, CONF_PORT
from homeassistant.core import HomeAssistant
from homeassistant.data_entry_flow import FlowResultType
from homeassistant.helpers.service_info.hassio import HassioServiceInfo

from custom_components.a9_cam.const import DOMAIN


async def test_user(hass: HomeAssistant, addon: int) -> None:
    result = await hass.config_entries.flow.async_init(DOMAIN, context={"source": SOURCE_USER})
    assert result["type"] is FlowResultType.FORM

    result = await hass.config_entries.flow.async_configure(
        result["flow_id"], {CONF_HOST: "127.0.0.1", CONF_PORT: addon}
    )
    assert result["type"] is FlowResultType.CREATE_ENTRY
    assert result["data"] == {CONF_HOST: "127.0.0.1", CONF_PORT: addon}


async def test_user_cannot_connect(hass: HomeAssistant, socket_enabled) -> None:
    result = await hass.config_entries.flow.async_init(DOMAIN, context={"source": SOURCE_USER})
    result = await hass.config_entries.flow.async_configure(
        result["flow_id"], {CONF_HOST: "127.0.0.1", CONF_PORT: 1}
    )
    assert result["type"] is FlowResultType.FORM
    assert result["errors"] == {"base": "cannot_connect"}


async def test_hassio_discovery(hass: HomeAssistant, addon: int) -> None:
    info = HassioServiceInfo(
        config={"host": "127.0.0.1", "port": addon}, name="A9 Camera", slug="a9-cam", uuid="x"
    )
    result = await hass.config_entries.flow.async_init(DOMAIN, context={"source": SOURCE_HASSIO}, data=info)
    assert result["type"] is FlowResultType.FORM
    assert result["step_id"] == "hassio_confirm"

    result = await hass.config_entries.flow.async_configure(result["flow_id"], {})
    assert result["type"] is FlowResultType.CREATE_ENTRY

    # the same add-on again is ignored
    result = await hass.config_entries.flow.async_init(DOMAIN, context={"source": SOURCE_HASSIO}, data=info)
    assert result["type"] is FlowResultType.ABORT

    # a manual second setup is refused
    result = await hass.config_entries.flow.async_init(DOMAIN, context={"source": SOURCE_USER})
    assert result["type"] is FlowResultType.ABORT


async def test_hassio_new_port_updates_entry(hass: HomeAssistant, addon: int) -> None:
    """An entry made for port 80 follows the add-on to its new announced port, without a new notice."""
    from pytest_homeassistant_custom_component.common import MockConfigEntry

    entry = MockConfigEntry(domain=DOMAIN, unique_id="127.0.0.1:80", data={CONF_HOST: "127.0.0.1", CONF_PORT: 80})
    entry.add_to_hass(hass)
    info = HassioServiceInfo(
        config={"host": "127.0.0.1", "port": addon}, name="A9 Camera", slug="a9-cam", uuid="x"
    )
    result = await hass.config_entries.flow.async_init(DOMAIN, context={"source": SOURCE_HASSIO}, data=info)
    assert result["type"] is FlowResultType.ABORT
    assert entry.data[CONF_PORT] == addon
    assert entry.unique_id == DOMAIN
