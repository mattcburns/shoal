"""Device-page tabs: Shoal Status, Events, Jobs, and Sensors.

Each view resolves the NetBox Device it's attached to and uses its numeric
primary key (str(device.pk)) as the Shoal device_id -- this is the identity
convention Shoal's own NetBox client (internal/common/netbox/client.go)
already establishes: UpsertDevice keys by serial, but the NetBox device ID
returned from that create/lookup becomes Shoal's device_id from then on. No
separate "shoal_device_id" custom field is needed.

Deploy also remaps lab hostnames (shoal-node-1) to this pk when NetBox is
configured, so jobs started with -device-id shoal-node-1 still appear here.

Write actions (start / cancel / power / credentials) POST back to this view
and call Shoal's /v1/jobs, power, and credentials APIs. BMC passwords are
never stored in NetBox: the credentials form PUTs to Shoal's secrets backend
(credential_ref only). Lab plugin config supplies non-secret defaults (BMC
URL, ISO URL).
"""

from django.conf import settings
from django.contrib import messages
from django.shortcuts import redirect
from django.utils.http import url_has_allowed_host_and_scheme

from dcim.models import Device
from netbox.views import generic
from utilities.views import ViewTab, register_model_view

from . import client
from .defaults import bmc_endpoint as resolve_bmc_endpoint
from .defaults import iso_url as resolve_iso_url
from .defaults import serial_transport as resolve_serial_transport
from .defaults import stall_timeout_ns as resolve_stall_timeout_ns
from .defaults import system_id as resolve_system_id

PLUGIN_NAME = "netbox_shoal"


def _cfg():
    return settings.PLUGINS_CONFIG.get(PLUGIN_NAME, {})


def _active_or_latest_job(jobs):
    """Prefer a provisioning job; else first row (API returns newest-first)."""
    if not jobs:
        return None
    for j in jobs:
        if (j or {}).get("state") == "provisioning":
            return j
    return jobs[0]


def _can_control(request):
    """Start/cancel require change_device (stronger than view-only tabs)."""
    return request.user.has_perm("dcim.change_device")


def _redirect_back(request, fallback_path):
    nxt = request.POST.get("next") or request.GET.get("next") or fallback_path
    if nxt and url_has_allowed_host_and_scheme(
        nxt, allowed_hosts={request.get_host()}, require_https=request.is_secure()
    ):
        return redirect(nxt)
    return redirect(fallback_path)


def _device_cf(instance):
    cf = getattr(instance, "custom_field_data", None) or {}
    if not isinstance(cf, dict):
        cf = {}
    extra = getattr(instance, "cf", None)
    if extra is not None and not cf:
        try:
            cf = dict(extra)
        except (TypeError, ValueError):
            cf = {}
    return cf


def _device_bmc_ip(instance):
    return (_device_cf(instance).get("bmc_ip") or "").strip()


def _device_credential_ref(instance):
    return (_device_cf(instance).get("credential_ref") or "").strip()


def _role_slug(instance):
    role = getattr(instance, "role", None)
    return (getattr(role, "slug", None) or "").strip()


def _start_defaults(instance):
    """Build form defaults from plugin config + device identity."""
    cfg = _cfg()
    name = instance.name or ""
    bmc_ip = _device_bmc_ip(instance)
    bmc_endpoint = resolve_bmc_endpoint(
        bmc_ip=bmc_ip,
        role_slug=_role_slug(instance),
        default_endpoint=(cfg.get("SHOAL_DEFAULT_BMC_ENDPOINT") or "").strip(),
    )
    role = _role_slug(instance)
    iso = resolve_iso_url(
        role_slug=role,
        default_iso=(cfg.get("SHOAL_DEFAULT_ISO_URL") or "").strip(),
        real_iso=(cfg.get("SHOAL_REAL_BMC_ISO_URL") or "").strip(),
    )
    return {
        "device_id": str(instance.pk),
        "serial_target": name,
        "system_id": resolve_system_id(role, name),
        "bmc_endpoint": bmc_endpoint,
        "iso_url": iso,
        "profile_ref": (cfg.get("SHOAL_DEFAULT_PROFILE_REF") or "spike").strip() or "spike",
        "serial_transport": resolve_serial_transport(role),
        "credential_ref": _device_credential_ref(instance),
        "stall_timeout": resolve_stall_timeout_ns(role),
    }


def _handle_control_post(request, instance):
    """Process start/cancel POST. Returns (messages_added: bool)."""
    if not _can_control(request):
        messages.error(request, "Permission denied: dcim.change_device required to start or cancel jobs.")
        return True

    action = (request.POST.get("action") or "").strip()
    if action == "start":
        defaults = _start_defaults(instance)
        body = {
            "device_id": defaults["device_id"],
            "serial_target": (request.POST.get("serial_target") or defaults["serial_target"]).strip(),
            "system_id": (request.POST.get("system_id") or defaults["system_id"]).strip(),
            "bmc_endpoint": (request.POST.get("bmc_endpoint") or defaults["bmc_endpoint"]).strip(),
            "iso_url": (request.POST.get("iso_url") or defaults["iso_url"]).strip(),
            "profile_ref": (request.POST.get("profile_ref") or defaults["profile_ref"]).strip(),
        }
        if defaults.get("serial_transport"):
            body["serial_transport"] = defaults["serial_transport"]
        if defaults.get("credential_ref"):
            body["credential_ref"] = defaults["credential_ref"]
        if defaults.get("stall_timeout"):
            body["stall_timeout"] = defaults["stall_timeout"]
        # Optional credential override (never stored in NetBox). Empty => stored
        # per-device secret, then SHOAL_BMC_*.
        user = (request.POST.get("bmc_username") or "").strip()
        password = request.POST.get("bmc_password") or ""
        if user:
            body["bmc_username"] = user
        if password:
            body["bmc_password"] = password
        if request.POST.get("approve_destruct") == "on":
            body["approve_destruct"] = True

        if not body["bmc_endpoint"]:
            messages.error(request, "BMC endpoint is required (set SHOAL_DEFAULT_BMC_ENDPOINT in plugin config or the form).")
            return True
        if not body["iso_url"] and body["profile_ref"] in ("", "spike"):
            messages.error(request, "ISO URL is required for spike jobs (set SHOAL_DEFAULT_ISO_URL or the form).")
            return True

        data, err = client.start_job(body)
        if err:
            job_id = ""
            if isinstance(data, dict) and isinstance(data.get("job"), dict):
                job_id = data["job"].get("id") or ""
            if job_id:
                messages.warning(request, "Job %s started but reported an error: %s" % (job_id[:12], err))
            else:
                messages.error(request, "Start failed: %s" % err)
            return True
        job_id = (data or {}).get("id") or "?"
        messages.success(
            request,
            "Provisioning job %s started (state=%s). Status auto-refreshes while provisioning."
            % (job_id[:16], (data or {}).get("state", "?")),
        )
        return True

    if action == "deprovision":
        defaults = _start_defaults(instance)
        body = {
            "device_id": defaults["device_id"],
            "kind": "deprovision",
            "prep": "wipe_only",
            "serial_target": (request.POST.get("serial_target") or defaults["serial_target"]).strip(),
            "system_id": (request.POST.get("system_id") or defaults["system_id"]).strip(),
            "bmc_endpoint": (request.POST.get("bmc_endpoint") or defaults["bmc_endpoint"]).strip(),
            "wipe_level": (request.POST.get("wipe_level") or "").strip(),
        }
        if defaults.get("serial_transport"):
            body["serial_transport"] = defaults["serial_transport"]
        if defaults.get("credential_ref"):
            body["credential_ref"] = defaults["credential_ref"]
        user = (request.POST.get("bmc_username") or "").strip()
        password = request.POST.get("bmc_password") or ""
        if user:
            body["bmc_username"] = user
        if password:
            body["bmc_password"] = password
        if request.POST.get("approve_destruct") == "on":
            body["approve_destruct"] = True

        if not body["bmc_endpoint"]:
            messages.error(request, "BMC endpoint is required (set SHOAL_DEFAULT_BMC_ENDPOINT in plugin config or the form).")
            return True
        if not body["wipe_level"]:
            messages.error(request, "Wipe level (discard or zero) is required to deprovision.")
            return True
        if not body.get("approve_destruct"):
            messages.error(request, "Confirmation is required: deprovision permanently wipes the boot disk.")
            return True

        # Mirrors start_job: same POST /v1/jobs, kind=deprovision on the wire.
        data, err = client.start_job(body)
        if err:
            job_id = ""
            if isinstance(data, dict) and isinstance(data.get("job"), dict):
                job_id = data["job"].get("id") or ""
            if job_id:
                messages.warning(request, "Deprovision job %s started but reported an error: %s" % (job_id[:12], err))
            else:
                messages.error(request, "Deprovision failed: %s" % err)
            return True
        job_id = (data or {}).get("id") or "?"
        messages.success(
            request,
            "Deprovision job %s started (state=%s). Status auto-refreshes while running."
            % (job_id[:16], (data or {}).get("state", "?")),
        )
        return True

    if action == "credentials":
        body = {
            "username": (request.POST.get("bmc_username") or "").strip(),
        }
        password = request.POST.get("bmc_password") or ""
        if password:
            body["password"] = password
        ip = (request.POST.get("bmc_ip") or "").strip()
        if ip:
            body["bmc_ip"] = ip
        data, err = client.put_credentials(str(instance.pk), body)
        if err:
            messages.error(request, "Save BMC credentials failed: %s" % err)
            return True
        messages.success(
            request,
            "BMC credentials saved in Shoal (ref=%s). Password is not stored in NetBox."
            % ((data or {}).get("credential_ref") or "?"),
        )
        return True

    if action == "power":
        defaults = _start_defaults(instance)
        reset_type = (request.POST.get("reset_type") or "").strip()
        body = {
            "reset_type": reset_type,
            "bmc_endpoint": (request.POST.get("bmc_endpoint") or defaults["bmc_endpoint"]).strip(),
            "system_id": (request.POST.get("system_id") or "").strip(),
        }
        user = (request.POST.get("bmc_username") or "").strip()
        password = request.POST.get("bmc_password") or ""
        if user:
            body["bmc_username"] = user
        if password:
            body["bmc_password"] = password
        if not body["bmc_endpoint"]:
            messages.error(request, "BMC endpoint is required for power control.")
            return True
        data, err = client.power_device(str(instance.pk), body)
        if err:
            messages.error(request, "Power %s failed: %s" % (reset_type or "action", err))
            return True
        state = (data or {}).get("power_state") or "?"
        messages.success(
            request,
            "Power %s sent. BMC reports power_state=%s." % (reset_type, state),
        )
        return True

    if action == "poll":
        defaults = _start_defaults(instance)
        body = {
            "bmc_endpoint": (request.POST.get("bmc_endpoint") or defaults["bmc_endpoint"]).strip(),
            "system_id": (request.POST.get("system_id") or "").strip(),
        }
        user = (request.POST.get("bmc_username") or "").strip()
        password = request.POST.get("bmc_password") or ""
        if user:
            body["bmc_username"] = user
        if password:
            body["bmc_password"] = password
        if not body["bmc_endpoint"]:
            messages.error(request, "BMC endpoint is required to poll sensors.")
            return True
        data, err = client.poll_device(str(instance.pk), body)
        if err:
            messages.error(request, "Poll BMC failed: %s" % err)
            return True
        messages.success(
            request,
            "Polled BMC: %s sensor(s), %s firmware, power=%s, %s new SEL."
            % (
                (data or {}).get("sensors_written", 0),
                (data or {}).get("firmware_written", 0),
                (data or {}).get("power_state") or "—",
                (data or {}).get("sel_new", 0),
            ),
        )
        return True

    if action == "cancel":
        job_id = (request.POST.get("job_id") or "").strip()
        if not job_id:
            messages.error(request, "Missing job id to cancel.")
            return True
        _, err = client.cancel_job(job_id)
        if err:
            messages.error(request, "Cancel failed: %s" % err)
        else:
            messages.success(request, "Cancel requested for job %s." % job_id[:16])
        return True

    messages.error(request, "Unknown action.")
    return True


def _status_context(instance, request=None):
    device_id = str(instance.pk)
    data, error = client.get_status(device_id)
    jobs_data, jobs_err = client.get_jobs(device_id, limit=5)
    jobs = (jobs_data or {}).get("jobs", []) if not jobs_err else []
    active = _active_or_latest_job(jobs)
    log_lines = []
    log_error = None
    if active and active.get("id"):
        log_data, log_error = client.get_job_log(active["id"], limit=40)
        log_lines = (log_data or {}).get("lines", [])
    provisioning = bool(
        data and (data.get("lifecycle_state") == "provisioning" or data.get("active_job_id"))
    ) or any((j or {}).get("state") == "provisioning" for j in jobs)
    defaults = _start_defaults(instance)
    power_system_id = ""
    if str(defaults.get("serial_target") or "").startswith("shoal-node"):
        power_system_id = defaults.get("system_id") or ""
    cf = getattr(instance, "custom_field_data", None) or {}
    if not isinstance(cf, dict):
        cf = {}
    creds, creds_err = client.get_credentials(
        device_id, credential_ref=(cf.get("credential_ref") or "")
    )
    profiles_data, profiles_err = client.get_profiles()
    # profiles_err (SHOAL_PROFILE_DIR unset, or Shoal unreachable) just means
    # the dropdown falls back to "spike" only -- not worth failing the page.
    profiles = (profiles_data or {}).get("profiles", []) if not profiles_err else []
    if isinstance(creds, dict):
        if not creds.get("bmc_ip") and cf.get("bmc_ip"):
            creds = dict(creds)
            creds["bmc_ip"] = cf.get("bmc_ip")
        if not creds.get("credential_ref") and cf.get("credential_ref"):
            creds = dict(creds)
            creds["credential_ref"] = cf.get("credential_ref")
    return {
        "shoal_status": data,
        "shoal_error": error,
        "shoal_jobs": jobs,
        "shoal_active_job": active,
        "shoal_job_log": log_lines,
        "shoal_job_log_error": log_error,
        "shoal_auto_refresh": provisioning,
        "shoal_can_control": bool(request and _can_control(request)),
        "shoal_start_defaults": defaults,
        "shoal_power_system_id": power_system_id,
        "shoal_credentials": creds or {},
        "shoal_credentials_error": creds_err,
        "shoal_actions_enabled": bool(_cfg().get("SHOAL_ENABLE_ACTIONS", True)),
        "shoal_profiles": profiles,
    }


@register_model_view(Device, name="shoal_status")
class ShoalStatusView(generic.ObjectView):
    queryset = Device.objects.all()
    template_name = "netbox_shoal/status.html"
    tab = ViewTab(
        label="Shoal Status",
        permission="dcim.view_device",
    )

    def get_extra_context(self, request, instance):
        return _status_context(instance, request)

    def post(self, request, **kwargs):
        instance = self.get_object(**kwargs)
        if _cfg().get("SHOAL_ENABLE_ACTIONS", True):
            _handle_control_post(request, instance)
        else:
            messages.error(request, "Shoal write actions are disabled in plugin config.")
        return _redirect_back(request, request.path)


@register_model_view(Device, name="shoal_events")
class ShoalEventsView(generic.ObjectView):
    queryset = Device.objects.all()
    template_name = "netbox_shoal/events.html"
    tab = ViewTab(
        label="Shoal Events",
        permission="dcim.view_device",
    )

    def get_extra_context(self, request, instance):
        data, error = client.get_events(str(instance.pk), limit=50)
        return {
            "shoal_events": (data or {}).get("events", []),
            "shoal_error": error,
        }


@register_model_view(Device, name="shoal_jobs")
class ShoalJobsView(generic.ObjectView):
    queryset = Device.objects.all()
    template_name = "netbox_shoal/jobs.html"
    tab = ViewTab(
        label="Shoal Jobs",
        permission="dcim.view_device",
    )

    def get_extra_context(self, request, instance):
        device_id = str(instance.pk)
        data, error = client.get_jobs(device_id, limit=50)
        jobs = (data or {}).get("jobs", [])
        active = _active_or_latest_job(jobs)
        log_lines = []
        log_error = None
        if active and active.get("id"):
            log_data, log_error = client.get_job_log(active["id"], limit=80)
            log_lines = (log_data or {}).get("lines", [])
        return {
            "shoal_jobs": jobs,
            "shoal_error": error,
            "shoal_active_job": active,
            "shoal_job_log": log_lines,
            "shoal_job_log_error": log_error,
            "shoal_auto_refresh": any((j or {}).get("state") == "provisioning" for j in jobs),
            "shoal_can_control": bool(request and _can_control(request)),
            "shoal_actions_enabled": bool(_cfg().get("SHOAL_ENABLE_ACTIONS", True)),
        }

    def post(self, request, **kwargs):
        instance = self.get_object(**kwargs)
        if _cfg().get("SHOAL_ENABLE_ACTIONS", True):
            _handle_control_post(request, instance)
        else:
            messages.error(request, "Shoal write actions are disabled in plugin config.")
        return _redirect_back(request, request.path)


@register_model_view(Device, name="shoal_sensors")
class ShoalSensorsView(generic.ObjectView):
    queryset = Device.objects.all()
    template_name = "netbox_shoal/sensors.html"
    tab = ViewTab(
        label="Shoal Sensors",
        permission="dcim.view_device",
    )

    def get_extra_context(self, request, instance):
        data, error = client.get_sensors(str(instance.pk), limit=200)
        defaults = _start_defaults(instance)
        power_system_id = ""
        if str(defaults.get("serial_target") or "").startswith("shoal-node"):
            power_system_id = defaults.get("system_id") or ""
        return {
            "shoal_sensors": (data or {}).get("readings", []),
            "shoal_error": error,
            "shoal_can_control": bool(request and _can_control(request)),
            "shoal_actions_enabled": bool(_cfg().get("SHOAL_ENABLE_ACTIONS", True)),
            "shoal_start_defaults": defaults,
            "shoal_power_system_id": power_system_id,
        }

    def post(self, request, **kwargs):
        instance = self.get_object(**kwargs)
        if _cfg().get("SHOAL_ENABLE_ACTIONS", True):
            _handle_control_post(request, instance)
        else:
            messages.error(request, "Shoal write actions are disabled in plugin config.")
        return _redirect_back(request, request.path)


@register_model_view(Device, name="shoal_firmware")
class ShoalFirmwareView(generic.ObjectView):
    queryset = Device.objects.all()
    template_name = "netbox_shoal/firmware.html"
    tab = ViewTab(
        label="Shoal Firmware",
        permission="dcim.view_device",
    )

    def get_extra_context(self, request, instance):
        data, error = client.get_firmware(str(instance.pk), limit=200)
        defaults = _start_defaults(instance)
        power_system_id = ""
        if str(defaults.get("serial_target") or "").startswith("shoal-node"):
            power_system_id = defaults.get("system_id") or ""
        return {
            "shoal_firmware": (data or {}).get("components", []),
            "shoal_firmware_ts": (data or {}).get("ts"),
            "shoal_error": error,
            "shoal_can_control": bool(request and _can_control(request)),
            "shoal_actions_enabled": bool(_cfg().get("SHOAL_ENABLE_ACTIONS", True)),
            "shoal_start_defaults": defaults,
            "shoal_power_system_id": power_system_id,
        }

    def post(self, request, **kwargs):
        instance = self.get_object(**kwargs)
        if _cfg().get("SHOAL_ENABLE_ACTIONS", True):
            _handle_control_post(request, instance)
        else:
            messages.error(request, "Shoal write actions are disabled in plugin config.")
        return _redirect_back(request, request.path)
