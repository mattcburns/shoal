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
