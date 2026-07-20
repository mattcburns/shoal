# Bakes the netbox-shoal plugin into the lab's NetBox image. Pinned to the
# same NetBox version as infra/ansible/roles/compose_stack (see that role's
# docker-compose.lab.yml.j2 for why: v4.6+ netbox-docker switched to v2 API
# tokens and breaks the bootstrap playbook's fixed-key token creation).
FROM docker.io/netboxcommunity/netbox:v4.5.10-4.0.2

# This image ships no `pip` at all (deliberately stripped -- deps are managed
# with `uv`, already on PATH). VIRTUAL_ENV is already set to the netbox venv,
# so a bare `uv pip install` targets it without extra flags.
COPY . /opt/netbox-shoal
RUN uv pip install --no-cache /opt/netbox-shoal
