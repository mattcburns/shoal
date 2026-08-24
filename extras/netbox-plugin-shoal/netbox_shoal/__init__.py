"""NetBox plugin: Shoal device telemetry tabs (status, events).

Renders Shoal (github.com/mattcburns/shoal) device status/events on the
NetBox device detail page, reading Shoal's HTTP API server-side. See
docs/netbox-telemetry-ui-design.md in the main Shoal repo for the design.
"""

from netbox.plugins import PluginConfig


class ShoalConfig(PluginConfig):
    name = "netbox_shoal"
    verbose_name = "Shoal Telemetry"
    description = "Shoal device status/events tabs on the NetBox device page"
    version = "0.4.0"
    author = "Shoal contributors"
    base_url = "shoal"

    # Read from PLUGINS_CONFIG["netbox_shoal"] in configuration.py.
    # SHOAL_BASE_URL empty means "not configured" -- views render a plain
    # message rather than erroring. SHOAL_API_TOKEN empty means no
    # Authorization header is sent, matching Shoal's own auth gate (which is
    # a no-op when SHOAL_API_TOKEN is unset -- the lab default).
    # Lab demo write path: SHOAL_DEFAULT_* prefill Start form; BMC passwords
    # stay on Shoal (SHOAL_BMC_*) unless the operator types them into the form.
    default_settings = {
        "SHOAL_BASE_URL": "",
        "SHOAL_API_TOKEN": "",
        "SHOAL_REQUEST_TIMEOUT": 30,  # Start can take longer (media + SOL register)
        "SHOAL_ENABLE_ACTIONS": True,
        "SHOAL_DEFAULT_BMC_ENDPOINT": "",
        "SHOAL_DEFAULT_ISO_URL": "",
        "SHOAL_REAL_BMC_ISO_URL": "",  # physical servers; BMC-reachable HTTP ISO
        "SHOAL_DEFAULT_PROFILE_REF": "spike",
    }


config = ShoalConfig
