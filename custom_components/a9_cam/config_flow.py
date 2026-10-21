"""Config flow: found by the add-on (Supervisor discovery) or entered by hand."""

from __future__ import annotations

from typing import Any

import voluptuous as vol

from homeassistant.config_entries import ConfigFlow, ConfigFlowResult
from homeassistant.const import CONF_HOST, CONF_PORT
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from homeassistant.helpers.service_info.hassio import HassioServiceInfo

from .api import A9Client, A9Error
from .const import DEFAULT_PORT, DOMAIN


class A9ConfigFlow(ConfigFlow, domain=DOMAIN):
    VERSION = 1

    _host: str
    _port: int

    async def _check(self, host: str, port: int) -> str | None:
        try:
            await A9Client(async_get_clientsession(self.hass), host, port).devices()
        except A9Error:
            return "cannot_connect"
        return None

    async def async_step_user(self, user_input: dict[str, Any] | None = None) -> ConfigFlowResult:
        errors: dict[str, str] = {}
        if user_input is not None:
            host, port = user_input[CONF_HOST], user_input[CONF_PORT]
            await self.async_set_unique_id(f"{host}:{port}")
            self._abort_if_unique_id_configured()
            if (error := await self._check(host, port)) is None:
                return self.async_create_entry(title="A9 Camera", data={CONF_HOST: host, CONF_PORT: port})
            errors["base"] = error

        return self.async_show_form(
            step_id="user",
            data_schema=vol.Schema(
                {
                    vol.Required(CONF_HOST, default="127.0.0.1"): str,
                    vol.Required(CONF_PORT, default=DEFAULT_PORT): int,
                }
            ),
            errors=errors,
        )

    async def async_step_hassio(self, discovery_info: HassioServiceInfo) -> ConfigFlowResult:
        """The add-on announced itself."""
        self._host = discovery_info.config["host"]
        self._port = discovery_info.config["port"]
        await self.async_set_unique_id(f"{self._host}:{self._port}")
        self._abort_if_unique_id_configured()
        return await self.async_step_hassio_confirm()

    async def async_step_hassio_confirm(self, user_input: dict[str, Any] | None = None) -> ConfigFlowResult:
        errors: dict[str, str] = {}
        if user_input is not None:
            if (error := await self._check(self._host, self._port)) is None:
                return self.async_create_entry(
                    title="A9 Camera", data={CONF_HOST: self._host, CONF_PORT: self._port}
                )
            errors["base"] = error
        return self.async_show_form(step_id="hassio_confirm", errors=errors)
