"""No plugin-specific URL patterns needed -- both views attach to the
existing dcim.Device model via @register_model_view(Device, name=...) in
views.py, and NetBox's own dcim/urls.py (get_model_urls('dcim', 'device'))
discovers and routes to them automatically.

The `from . import views` below is not unused despite nothing referencing
it directly: it's what makes Django actually execute views.py (and thus run
the @register_model_view decorators) at plugin load time. Confirmed the
hard way -- without this import, nothing ever imports views.py, the
decorators never run, and the tabs silently don't exist (no 500, no log
line, just a 404 on every guessed URL).
"""

from . import views  # noqa: F401

urlpatterns = []
