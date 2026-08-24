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

from netbox_shoal.defaults import (  # noqa: E402
    bmc_endpoint,
    iso_url,
    serial_transport,
    stall_timeout_ns,
    system_id,
)


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


class ISOAndTransportTests(unittest.TestCase):
    def test_lab_keeps_default_iso_and_no_transport(self):
        self.assertEqual(
            iso_url(
                role_slug="virtual-bmc-node",
                default_iso="http://192.168.124.1:8080/a.iso",
                real_iso="http://172.16.20.138:8080/a.iso",
            ),
            "http://192.168.124.1:8080/a.iso",
        )
        self.assertEqual(serial_transport("virtual-bmc-node"), "")

    def test_physical_uses_real_iso_and_redfish_sol(self):
        self.assertEqual(
            iso_url(
                role_slug="server",
                default_iso="http://192.168.124.1:8080/a.iso",
                real_iso="http://172.16.20.138:8080/a.iso",
            ),
            "http://172.16.20.138:8080/a.iso",
        )
        self.assertEqual(serial_transport("server"), "redfish_sol")

    def test_physical_falls_back_to_lab_iso_when_real_unset(self):
        self.assertEqual(
            iso_url(role_slug="server", default_iso="http://lab/a.iso", real_iso=""),
            "http://lab/a.iso",
        )


class SystemIDTests(unittest.TestCase):
    def test_lab_virtual_node_uses_device_name(self):
        # Shared sushy emulator hosts multiple Systems; System.Name == the
        # libvirt domain name, so an explicit match is required.
        self.assertEqual(system_id("virtual-bmc-node", "shoal-node-1"), "shoal-node-1")

    def test_physical_server_leaves_blank_for_orchestrator_autoresolve(self):
        # Real BMCs vary their System ID by vendor (Dell System.Embedded.1,
        # others differ). A real device's name (e.g. a service tag) is never
        # the Redfish System ID -- leave blank so the orchestrator resolves
        # the BMC's single System itself instead of guessing.
        self.assertEqual(system_id("server", "C784MH3"), "")

    def test_empty(self):
        self.assertEqual(system_id(), "")


class StallTimeoutTests(unittest.TestCase):
    def test_lab_virtual_node_uses_orchestrator_default(self):
        # 0 => omitted from the request body; orchestrator's own
        # DefaultSOLStall (3m) applies, which is fine for fast lab boot.
        self.assertEqual(stall_timeout_ns("virtual-bmc-node"), 0)

    def test_physical_server_gets_15_minute_budget_in_nanoseconds(self):
        # StartJobRequest.StallTimeout is a raw Go time.Duration over JSON
        # (nanoseconds), not a string like "15m".
        self.assertEqual(stall_timeout_ns("server"), 15 * 60 * 1_000_000_000)

    def test_empty(self):
        self.assertEqual(stall_timeout_ns(), 15 * 60 * 1_000_000_000)


if __name__ == "__main__":
    unittest.main()
