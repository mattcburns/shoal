"""Form defaults for Shoal power/provision actions.

No Django imports so this is unit-testable outside NetBox.
"""

# Lab sushy nodes share one emulator; their NetBox bmc_ip is the libvirt
# address, not the Redfish URL. Real servers use https://bmc_ip instead.
LAB_VIRTUAL_ROLE = "virtual-bmc-node"


def endpoint_from_bmc_ip(bmc_ip):
    """Turn a bmc_ip custom field into a Redfish base URL."""
    ip = (bmc_ip or "").strip()
    if not ip:
        return ""
    if ip.startswith("http://") or ip.startswith("https://"):
        return ip
    return "https://%s" % ip


def is_lab_virtual(role_slug=""):
    """True for nested-lab sushy nodes (shared Virtual BMC Node role)."""
    return (role_slug or "").strip() == LAB_VIRTUAL_ROLE


def iso_url(role_slug="", default_iso="", real_iso=""):
    """Prefill ISO URL: real BMCs use real_iso when set, else the lab default."""
    default_iso = (default_iso or "").strip()
    real_iso = (real_iso or "").strip()
    if not is_lab_virtual(role_slug) and real_iso:
        return real_iso
    return default_iso


def system_id(role_slug="", device_name=""):
    """Redfish System ID to prefill.

    Lab sushy nodes share one emulator hosting multiple Systems, so an
    explicit name match is required (System.Name == the libvirt domain
    name there). Physical BMCs vary their System ID by vendor (Dell
    System.Embedded.1, others differ) but almost always expose exactly
    one System — leave this blank and let the orchestrator auto-resolve
    it (internal/deploy/job/orchestrator.go retries with an empty lookup
    when the request's system_id is empty and the serial_target guess
    doesn't match) rather than guessing a vendor-specific ID here.
    """
    if is_lab_virtual(role_slug):
        return (device_name or "").strip()
    return ""


def stall_timeout_ns(role_slug=""):
    """SOL silence window before a job is marked stalled, in nanoseconds
    (StartJobRequest.StallTimeout is a raw Go time.Duration over JSON, so
    this must be a plain integer, not a string like "15m").

    Real BMC virtual-media boot (BIOS POST + UEFI CD boot) commonly takes
    several minutes before the marker init ever runs and emits a first
    SHOAL| line. The orchestrator's built-in default (3 minutes --
    internal/deploy/job/orchestrator.go DefaultSOLStall) is tuned for the
    fast nested-lab sushy/libvirt boot and is routinely too short for
    physical hardware. Measured live on a PowerEdge R750: warm restart
    reaches markers in ~7-9 minutes, but a COLD power-on takes ~25 minutes
    of POST (memory training + repeated Lifecycle Controller cycles, with
    console-silent stretches longer than 15 minutes) before the boot
    device is even selected. Since the watch resets its stall timer on any
    SOL activity (not just markers), this bound only fires on true console
    silence -- so a generous 30 minutes is safe and covers the cold path.
    """
    if is_lab_virtual(role_slug):
        return 0
    return 30 * 60 * 1_000_000_000


def serial_transport(role_slug=""):
    """redfish_sol for physical servers; empty for lab virtual BMC nodes (libvirt)."""
    if is_lab_virtual(role_slug):
        return ""
    return "redfish_sol"


def bmc_endpoint(bmc_ip="", role_slug="", default_endpoint=""):
    """Choose the BMC URL to prefill in NetBox forms.

    Per-device bmc_ip wins for anything that is not a lab Virtual BMC Node.
    Lab virtual nodes keep SHOAL_DEFAULT_BMC_ENDPOINT (shared sushy).
    """
    bmc_ip = (bmc_ip or "").strip()
    role_slug = (role_slug or "").strip()
    default_endpoint = (default_endpoint or "").strip()
    if bmc_ip and role_slug != LAB_VIRTUAL_ROLE:
        return endpoint_from_bmc_ip(bmc_ip)
    if default_endpoint:
        return default_endpoint
    return endpoint_from_bmc_ip(bmc_ip)
