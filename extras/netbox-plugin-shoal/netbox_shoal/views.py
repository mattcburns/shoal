"""Device-page tabs: Shoal Status, Events, Jobs, and Sensors.

Each view resolves the NetBox Device it's attached to and uses its numeric
primary key (str(device.pk)) as the Shoal device_id -- this is the identity
convention Shoal's own NetBox client (internal/common/netbox/client.go)
already establishes: UpsertDevice keys by serial, but the NetBox device ID
returned from that create/lookup becomes Shoal's device_id from then on. No
separate "shoal_device_id" custom field is needed.
"""

from dcim.models import Device
from netbox.views import generic
from utilities.views import ViewTab, register_model_view

from . import client


@register_model_view(Device, name="shoal_status")
class ShoalStatusView(generic.ObjectView):
    queryset = Device.objects.all()
    template_name = "netbox_shoal/status.html"
    tab = ViewTab(
        label="Shoal Status",
        permission="dcim.view_device",
    )

    def get_extra_context(self, request, instance):
        data, error = client.get_status(str(instance.pk))
        return {
            "shoal_status": data,
            "shoal_error": error,
        }


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
        data, error = client.get_jobs(str(instance.pk), limit=50)
        return {
            "shoal_jobs": (data or {}).get("jobs", []),
            "shoal_error": error,
        }


@register_model_view(Device, name="shoal_sensors")
class ShoalSensorsView(generic.ObjectView):
    queryset = Device.objects.all()
    template_name = "netbox_shoal/sensors.html"
    tab = ViewTab(
        label="Shoal Sensors",
        permission="dcim.view_device",
    )

    def get_extra_context(self, request, instance):
        data, error = client.get_sensors(str(instance.pk), limit=50)
        return {
            "shoal_sensors": (data or {}).get("readings", []),
            "shoal_error": error,
        }
