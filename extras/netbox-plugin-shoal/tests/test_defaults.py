"""Unit tests for BMC endpoint prefill (no NetBox/Django required)."""

import sys
import types
import unittest

# netbox_shoal/__init__.py imports PluginConfig at package import time.
if "netbox.plugins" not in sys.modules:
    fake_netbox = types.ModuleType("netbox")
    fake_netbox_plugins = types.ModuleType("netbox.plugins")
    fake_netbox_plugins.PluginConfig = object
    fake_netbox.plugins = fake_netbox_plugins
    sys.modules["netbox"] = fake_netbox
    sys.modules["netbox.plugins"] = fake_netbox_plugins

from netbox_shoal.defaults import bmc_endpoint  # noqa: E402


class BMCEndpointTests(unittest.TestCase):
    def test_physical_server_uses_device_bmc_ip_not_lab_default(self):
        got = bmc_endpoint(
            bmc_ip="172.16.21.202",
            role_slug="server",
            default_endpoint="http://127.0.0.1:8001",
        )
        self.assertEqual(got, "https://172.16.21.202")

    def test_lab_virtual_node_keeps_shared_sushy_url(self):
        got = bmc_endpoint(
            bmc_ip="192.168.124.10",
            role_slug="virtual-bmc-node",
            default_endpoint="http://127.0.0.1:8001",
        )
        self.assertEqual(got, "http://127.0.0.1:8001")

    def test_lab_virtual_falls_back_to_bmc_ip_when_no_default(self):
        got = bmc_endpoint(bmc_ip="192.168.124.10", role_slug="virtual-bmc-node")
        self.assertEqual(got, "https://192.168.124.10")

    def test_existing_scheme_kept(self):
        got = bmc_endpoint(
            bmc_ip="http://172.16.21.202:8000",
            role_slug="server",
            default_endpoint="http://127.0.0.1:8001",
        )
        self.assertEqual(got, "http://172.16.21.202:8000")

    def test_empty(self):
        self.assertEqual(bmc_endpoint(), "")


if __name__ == "__main__":
    unittest.main()
