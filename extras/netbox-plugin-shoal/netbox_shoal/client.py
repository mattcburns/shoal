"""Thin HTTP client for the Shoal API, used by the plugin's views.

Deliberately minimal: no retries, no caching, one request per call. Base URL
and token come only from PLUGINS_CONFIG (admin-configured), never from
request input -- the plugin never proxies an operator-supplied host, so
there's no SSRF surface here by construction.

Returns (data, error) tuples instead of raising so views can render a plain
"Shoal unavailable" message rather than a 500 when Shoal is down, unreachable,
or simply not configured yet.
"""

import requests
from django.conf import settings

PLUGIN_NAME = "netbox_shoal"


def _cfg():
    return settings.PLUGINS_CONFIG.get(PLUGIN_NAME, {})


def _headers():
    headers = {"Content-Type": "application/json", "Accept": "application/json"}
    token = _cfg().get("SHOAL_API_TOKEN")
    if token:
        headers["Authorization"] = "Bearer " + token
    return headers


def _base():
    return (_cfg().get("SHOAL_BASE_URL") or "").rstrip("/")


def _timeout():
    return _cfg().get("SHOAL_REQUEST_TIMEOUT", 10)


def _get(path, params=None):
    base = _base()
    if not base:
        return None, "Shoal base URL not configured (SHOAL_BASE_URL)"

    try:
        resp = requests.get(
            base + path, headers=_headers(), params=params, timeout=_timeout()
        )
        resp.raise_for_status()
        return resp.json(), None
    except requests.RequestException as exc:
        return None, str(exc)


def _put(path, body=None):
    base = _base()
    if not base:
        return None, "Shoal base URL not configured (SHOAL_BASE_URL)"
    try:
        resp = requests.put(
            base + path,
            headers=_headers(),
            json=body or {},
            timeout=_timeout(),
        )
        if resp.status_code >= 400:
            try:
                data = resp.json()
            except ValueError:
                data = None
            msg = None
            if isinstance(data, dict):
                msg = data.get("error") or data.get("detail")
            if not msg:
                msg = "HTTP %s: %s" % (resp.status_code, (resp.text or "")[:200])
            return data, msg
        if resp.status_code == 204 or not resp.content:
            return {}, None
        return resp.json(), None
    except requests.RequestException as exc:
        return None, str(exc)


def _post(path, body=None, timeout=None):
    base = _base()
    if not base:
        return None, "Shoal base URL not configured (SHOAL_BASE_URL)"
    if timeout is None:
        timeout = _timeout()

    try:
        resp = requests.post(
            base + path,
            headers=_headers(),
            json=body or {},
            timeout=timeout,
        )
        # Start may return 409 with a partial job body on late BMC/SOL failure.
        if resp.status_code >= 400:
            try:
                data = resp.json()
            except ValueError:
                data = None
            msg = None
            if isinstance(data, dict):
                msg = data.get("error") or data.get("detail")
            if not msg:
                msg = "HTTP %s: %s" % (resp.status_code, (resp.text or "")[:200])
            return data, msg
        if resp.status_code == 204 or not resp.content:
            return {}, None
        return resp.json(), None
    except requests.RequestException as exc:
        return None, str(exc)


def get_profiles():
    """GET /v1/profiles -> ({"profiles": [{"profile": {...}, ...}]}, error)."""
    return _get("/v1/profiles")


def get_status(device_id):
    """GET /v1/devices/{id}/status -> (models.DeviceStatus dict, error)."""
    return _get("/v1/devices/%s/status" % device_id)


def get_events(device_id, limit=50):
    """GET /v1/devices/{id}/events?limit= -> ({"device_id","events":[...]}, error)."""
    return _get("/v1/devices/%s/events" % device_id, params={"limit": limit})


def get_jobs(device_id, limit=50, state=None):
    """GET /v1/devices/{id}/jobs?limit=&state= -> ({"device_id","jobs":[...]}, error)."""
    params = {"limit": limit}
    if state:
        params["state"] = state
    return _get("/v1/devices/%s/jobs" % device_id, params=params)


def get_sensors(device_id, limit=200):
    """GET /v1/devices/{id}/sensors?limit= -> ({"device_id","sensors":[...]}, error)."""
    return _get("/v1/devices/%s/sensors" % device_id, params={"limit": limit})


def get_firmware(device_id, limit=200):
    """GET /v1/devices/{id}/firmware?limit= -> ({"device_id","firmware":[...]}, error)."""
    return _get("/v1/devices/%s/firmware" % device_id, params={"limit": limit})


def get_job_log(job_id, limit=100):
    """GET /v1/jobs/{id}/log?limit= -> ({"job_id","log":[...]}, error)."""
    return _get("/v1/jobs/%s/log" % job_id, params={"limit": limit})


def start_job(body):
    """POST /v1/jobs with a StartJobRequest body -> (job dict or error body, error)."""
    return _post("/v1/jobs", body=body)


def cancel_job(job_id):
    """POST /v1/jobs/{id}/cancel -> (body, error)."""
    return _post("/v1/jobs/%s/cancel" % job_id, body={})


def power_device(device_id, body):
    """POST /v1/devices/{id}/power -> (result dict, error)."""
    return _post("/v1/devices/%s/power" % device_id, body=body)


def get_credentials(device_id, credential_ref=None):
    """GET /v1/devices/{id}/credentials -> view without password.

    credential_ref from NetBox CF avoids Shoal calling NetBox back during a
    device-page render (that nested round-trip can stall the Status tab).
    """
    params = None
    if credential_ref:
        params = {"credential_ref": credential_ref}
    return _get("/v1/devices/%s/credentials" % device_id, params=params)


def put_credentials(device_id, body):
    """PUT /v1/devices/{id}/credentials -> view without password."""
    return _put("/v1/devices/%s/credentials" % device_id, body=body)


def poll_device(device_id, body):
    """POST /v1/devices/{id}/poll -> on-demand SEL + sensor refresh."""
    # iDRAC ListSEL/ListSensors can exceed the default 30s plugin timeout.
    timeout = _timeout()
    if timeout < 120:
        timeout = 120
    return _post("/v1/devices/%s/poll" % device_id, body=body, timeout=timeout)
