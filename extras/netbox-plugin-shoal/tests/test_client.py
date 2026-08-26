"""Unit tests for netbox_shoal.client.

Django-settings-only: no NetBox app registry, no models, no database needed.
`django.conf.settings.configure()` is enough to exercise the plugin-config
reading + requests-mocking logic in isolation.
"""

import sys
import types
import unittest
from unittest import mock

import django
from django.conf import settings

if not settings.configured:
    settings.configure(
        PLUGINS_CONFIG={
            "netbox_shoal": {
                "SHOAL_BASE_URL": "http://shoal.example:8088",
                "SHOAL_API_TOKEN": "",
                "SHOAL_REQUEST_TIMEOUT": 10,
            }
        }
    )
    django.setup()

# netbox_shoal/__init__.py imports `netbox.plugins.PluginConfig` at package
# import time -- that module only exists inside a running NetBox instance.
# client.py itself has no NetBox dependency (only requests + django.conf),
# so stub the minimum needed to import the package outside NetBox rather
# than requiring a full NetBox install just to unit-test the HTTP client.
if "netbox.plugins" not in sys.modules:
    fake_netbox = types.ModuleType("netbox")
    fake_netbox_plugins = types.ModuleType("netbox.plugins")
    fake_netbox_plugins.PluginConfig = object
    fake_netbox.plugins = fake_netbox_plugins
    sys.modules["netbox"] = fake_netbox
    sys.modules["netbox.plugins"] = fake_netbox_plugins

from netbox_shoal import client  # noqa: E402  (import after settings.configure + stub)


class FakeResponse:
    def __init__(self, json_data, status_code=200):
        self._json = json_data
        self.status_code = status_code
        self.content = b"x" if json_data is not None else b""

    def raise_for_status(self):
        if self.status_code >= 400:
            import requests

            raise requests.HTTPError("status %d" % self.status_code)

    def json(self):
        return self._json


class GetStatusTests(unittest.TestCase):
    def setUp(self):
        # Reset PLUGINS_CONFIG to a known-good base before each test.
        settings.PLUGINS_CONFIG = {
            "netbox_shoal": {
                "SHOAL_BASE_URL": "http://shoal.example:8088",
                "SHOAL_API_TOKEN": "",
                "SHOAL_REQUEST_TIMEOUT": 10,
            }
        }

    @mock.patch("netbox_shoal.client.requests.get")
    def test_get_status_success(self, mock_get):
        mock_get.return_value = FakeResponse({"device_id": "1", "lifecycle_state": "provisioned"})
        data, err = client.get_status("1")
        self.assertIsNone(err)
        self.assertEqual(data["lifecycle_state"], "provisioned")
        mock_get.assert_called_once()
        args, kwargs = mock_get.call_args
        self.assertEqual(args[0], "http://shoal.example:8088/v1/devices/1/status")
        self.assertNotIn("Authorization", kwargs["headers"])

    @mock.patch("netbox_shoal.client.requests.get")
    def test_get_status_sends_bearer_token_when_configured(self, mock_get):
        settings.PLUGINS_CONFIG["netbox_shoal"]["SHOAL_API_TOKEN"] = "secret-token"
        mock_get.return_value = FakeResponse({"device_id": "1"})
        client.get_status("1")
        _, kwargs = mock_get.call_args
        self.assertEqual(kwargs["headers"]["Authorization"], "Bearer secret-token")

    def test_get_status_without_base_url_returns_error_not_exception(self):
        settings.PLUGINS_CONFIG["netbox_shoal"]["SHOAL_BASE_URL"] = ""
        data, err = client.get_status("1")
        self.assertIsNone(data)
        self.assertIn("not configured", err)

    @mock.patch("netbox_shoal.client.requests.get")
    def test_get_status_network_error_returns_tuple_not_raises(self, mock_get):
        import requests

        mock_get.side_effect = requests.ConnectionError("connection refused")
        data, err = client.get_status("1")
        self.assertIsNone(data)
        self.assertIn("connection refused", err)

    @mock.patch("netbox_shoal.client.requests.get")
    def test_get_status_http_error_returns_tuple_not_raises(self, mock_get):
        mock_get.return_value = FakeResponse({"error": "internal error"}, status_code=500)
        data, err = client.get_status("1")
        self.assertIsNone(data)
        self.assertIsNotNone(err)


class GetEventsTests(unittest.TestCase):
    def setUp(self):
        settings.PLUGINS_CONFIG = {
            "netbox_shoal": {
                "SHOAL_BASE_URL": "http://shoal.example:8088",
                "SHOAL_API_TOKEN": "",
                "SHOAL_REQUEST_TIMEOUT": 10,
            }
        }

    @mock.patch("netbox_shoal.client.requests.get")
    def test_get_events_passes_limit_param(self, mock_get):
        mock_get.return_value = FakeResponse({"device_id": "1", "events": []})
        client.get_events("1", limit=25)
        _, kwargs = mock_get.call_args
        self.assertEqual(kwargs["params"], {"limit": 25})

    @mock.patch("netbox_shoal.client.requests.get")
    def test_get_events_default_limit(self, mock_get):
        mock_get.return_value = FakeResponse({"device_id": "1", "events": []})
        client.get_events("1")
        _, kwargs = mock_get.call_args
        self.assertEqual(kwargs["params"], {"limit": 50})


class GetProfilesTests(unittest.TestCase):
    def setUp(self):
        settings.PLUGINS_CONFIG = {
            "netbox_shoal": {
                "SHOAL_BASE_URL": "http://shoal.example:8088",
                "SHOAL_API_TOKEN": "",
                "SHOAL_REQUEST_TIMEOUT": 10,
            }
        }

    @mock.patch("netbox_shoal.client.requests.get")
    def test_get_profiles_success(self, mock_get):
        mock_get.return_value = FakeResponse(
            {"profiles": [{"profile": {"ref": "lab-1-ubuntu", "os_family": "ubuntu"}}]}
        )
        data, err = client.get_profiles()
        self.assertIsNone(err)
        self.assertEqual(data["profiles"][0]["profile"]["ref"], "lab-1-ubuntu")
        args, kwargs = mock_get.call_args
        self.assertEqual(args[0], "http://shoal.example:8088/v1/profiles")
        self.assertIsNone(kwargs["params"])

    def test_get_profiles_without_base_url_returns_error_not_exception(self):
        settings.PLUGINS_CONFIG["netbox_shoal"]["SHOAL_BASE_URL"] = ""
        data, err = client.get_profiles()
        self.assertIsNone(data)
        self.assertIn("not configured", err)


class GetJobsTests(unittest.TestCase):
    def setUp(self):
        settings.PLUGINS_CONFIG = {
            "netbox_shoal": {
                "SHOAL_BASE_URL": "http://shoal.example:8088",
                "SHOAL_API_TOKEN": "",
                "SHOAL_REQUEST_TIMEOUT": 10,
            }
        }

    @mock.patch("netbox_shoal.client.requests.get")
    def test_get_jobs_url_and_default_limit(self, mock_get):
        mock_get.return_value = FakeResponse({"device_id": "1", "jobs": []})
        client.get_jobs("1")
        args, kwargs = mock_get.call_args
        self.assertEqual(args[0], "http://shoal.example:8088/v1/devices/1/jobs")
        self.assertEqual(kwargs["params"], {"limit": 50})

    @mock.patch("netbox_shoal.client.requests.get")
    def test_get_jobs_omits_state_when_not_given(self, mock_get):
        mock_get.return_value = FakeResponse({"device_id": "1", "jobs": []})
        client.get_jobs("1", limit=10)
        _, kwargs = mock_get.call_args
        self.assertNotIn("state", kwargs["params"])

    @mock.patch("netbox_shoal.client.requests.get")
    def test_get_jobs_passes_state_filter(self, mock_get):
        mock_get.return_value = FakeResponse({"device_id": "1", "jobs": []})
        client.get_jobs("1", limit=10, state="failed")
        _, kwargs = mock_get.call_args
        self.assertEqual(kwargs["params"], {"limit": 10, "state": "failed"})

    @mock.patch("netbox_shoal.client.requests.get")
    def test_get_jobs_returns_data_on_success(self, mock_get):
        mock_get.return_value = FakeResponse(
            {"device_id": "1", "jobs": [{"id": "j1", "state": "provisioning"}]}
        )
        data, err = client.get_jobs("1")
        self.assertIsNone(err)
        self.assertEqual(data["jobs"][0]["id"], "j1")


class GetSensorsTests(unittest.TestCase):
    def setUp(self):
        settings.PLUGINS_CONFIG = {
            "netbox_shoal": {
                "SHOAL_BASE_URL": "http://shoal.example:8088",
                "SHOAL_API_TOKEN": "",
                "SHOAL_REQUEST_TIMEOUT": 10,
            }
        }

    @mock.patch("netbox_shoal.client.requests.get")
    def test_get_sensors_url_and_default_limit(self, mock_get):
        mock_get.return_value = FakeResponse({"device_id": "1", "readings": []})
        client.get_sensors("1")
        args, kwargs = mock_get.call_args
        self.assertEqual(args[0], "http://shoal.example:8088/v1/devices/1/sensors")
        self.assertEqual(kwargs["params"], {"limit": 200})

    @mock.patch("netbox_shoal.client.requests.get")
    def test_get_sensors_passes_limit(self, mock_get):
        mock_get.return_value = FakeResponse({"device_id": "1", "readings": []})
        client.get_sensors("1", limit=5)
        _, kwargs = mock_get.call_args
        self.assertEqual(kwargs["params"], {"limit": 5})

    @mock.patch("netbox_shoal.client.requests.get")
    def test_get_sensors_returns_data_on_success(self, mock_get):
        mock_get.return_value = FakeResponse(
            {"device_id": "1", "readings": [{"sensor": "Inlet Temp", "value": 24.5, "unit": "Cel"}]}
        )
        data, err = client.get_sensors("1")
        self.assertIsNone(err)
        self.assertEqual(data["readings"][0]["sensor"], "Inlet Temp")


class GetJobLogTests(unittest.TestCase):
    def setUp(self):
        settings.PLUGINS_CONFIG = {
            "netbox_shoal": {
                "SHOAL_BASE_URL": "http://shoal.example:8088",
                "SHOAL_API_TOKEN": "",
                "SHOAL_REQUEST_TIMEOUT": 10,
            }
        }

    @mock.patch("netbox_shoal.client.requests.get")
    def test_get_job_log_url_and_limit(self, mock_get):
        mock_get.return_value = FakeResponse({"job_id": "abc", "lines": []})
        data, err = client.get_job_log("abc", limit=40)
        self.assertIsNone(err)
        self.assertEqual(data["job_id"], "abc")
        args, kwargs = mock_get.call_args
        self.assertEqual(args[0], "http://shoal.example:8088/v1/jobs/abc/log")
        self.assertEqual(kwargs["params"], {"limit": 40})


class WriteJobTests(unittest.TestCase):
    def setUp(self):
        settings.PLUGINS_CONFIG = {
            "netbox_shoal": {
                "SHOAL_BASE_URL": "http://shoal.example:8088",
                "SHOAL_API_TOKEN": "tok",
                "SHOAL_REQUEST_TIMEOUT": 10,
            }
        }

    @mock.patch("netbox_shoal.client.requests.post")
    def test_start_job_posts_json(self, mock_post):
        mock_post.return_value = FakeResponse({"id": "j1", "state": "provisioning"}, status_code=201)
        body = {"device_id": "1", "bmc_endpoint": "http://bmc", "iso_url": "http://iso/x.iso", "serial_target": "n1"}
        data, err = client.start_job(body)
        self.assertIsNone(err)
        self.assertEqual(data["id"], "j1")
        args, kwargs = mock_post.call_args
        self.assertEqual(args[0], "http://shoal.example:8088/v1/jobs")
        self.assertEqual(kwargs["json"], body)
        self.assertEqual(kwargs["headers"]["Authorization"], "Bearer tok")

    @mock.patch("netbox_shoal.client.requests.post")
    def test_start_job_conflict_returns_error_and_body(self, mock_post):
        mock_post.return_value = FakeResponse(
            {"error": "register watch failed", "job": {"id": "j2"}}, status_code=409
        )
        data, err = client.start_job({"device_id": "1"})
        self.assertIsNotNone(err)
        self.assertIn("register watch", err)
        self.assertEqual(data["job"]["id"], "j2")

    @mock.patch("netbox_shoal.client.requests.post")
    def test_cancel_job(self, mock_post):
        mock_post.return_value = FakeResponse({"ok": True}, status_code=200)
        data, err = client.cancel_job("abc")
        self.assertIsNone(err)
        args, _ = mock_post.call_args
        self.assertEqual(args[0], "http://shoal.example:8088/v1/jobs/abc/cancel")

    @mock.patch("netbox_shoal.client.requests.post")
    def test_power_device_posts_json(self, mock_post):
        mock_post.return_value = FakeResponse(
            {"device_id": "6", "reset_type": "On", "power_state": "On"}
        )
        body = {"reset_type": "On", "bmc_endpoint": "https://bmc"}
        data, err = client.power_device("6", body)
        self.assertIsNone(err)
        self.assertEqual(data["power_state"], "On")
        args, kwargs = mock_post.call_args
        self.assertEqual(args[0], "http://shoal.example:8088/v1/devices/6/power")
        self.assertEqual(kwargs["json"], body)

    @mock.patch("netbox_shoal.client.requests.get")
    def test_get_credentials(self, mock_get):
        mock_get.return_value = FakeResponse(
            {"device_id": "6", "username": "root", "has_password": True, "credential_ref": "bmc-C784MH3"}
        )
        data, err = client.get_credentials("6", credential_ref="bmc-C784MH3")
        self.assertIsNone(err)
        self.assertEqual(data["username"], "root")
        self.assertNotIn("password", data)
        args, kwargs = mock_get.call_args
        self.assertEqual(args[0], "http://shoal.example:8088/v1/devices/6/credentials")
        self.assertEqual(kwargs.get("params"), {"credential_ref": "bmc-C784MH3"})

    @mock.patch("netbox_shoal.client.requests.get")
    def test_get_firmware(self, mock_get):
        mock_get.return_value = FakeResponse(
            {"device_id": "6", "components": [{"id": "BIOS", "version": "1.8.0"}]}
        )
        data, err = client.get_firmware("6")
        self.assertIsNone(err)
        self.assertEqual(data["components"][0]["version"], "1.8.0")
        args, _ = mock_get.call_args
        self.assertEqual(args[0], "http://shoal.example:8088/v1/devices/6/firmware")

    @mock.patch("netbox_shoal.client.requests.post")
    def test_poll_device(self, mock_post):
        mock_post.return_value = FakeResponse(
            {"device_id": "6", "sel_new": 0, "sensors_written": 26}
        )
        body = {"bmc_endpoint": "https://172.16.21.202"}
        data, err = client.poll_device("6", body)
        self.assertIsNone(err)
        self.assertEqual(data["sensors_written"], 26)
        args, kwargs = mock_post.call_args
        self.assertEqual(args[0], "http://shoal.example:8088/v1/devices/6/poll")
        self.assertEqual(kwargs["json"], body)
        self.assertGreaterEqual(kwargs["timeout"], 120)

    @mock.patch("netbox_shoal.client.requests.put")
    def test_put_credentials(self, mock_put):
        mock_put.return_value = FakeResponse(
            {"device_id": "6", "username": "root", "has_password": True, "credential_ref": "bmc-C784MH3"}
        )
        data, err = client.put_credentials("6", {"username": "root", "password": "x"})
        self.assertIsNone(err)
        self.assertTrue(data["has_password"])
        self.assertNotIn("password", data)
        args, kwargs = mock_put.call_args
        self.assertEqual(args[0], "http://shoal.example:8088/v1/devices/6/credentials")


if __name__ == "__main__":
    unittest.main()
