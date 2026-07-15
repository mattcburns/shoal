// Package iso builds and publishes live images for Virtual Media boot.
//
// Phase 2: Ansible/lab nginx serves a prebuilt marker ISO on :8080.
// Phase 5c: Go-owned Builder wraps the known-good build-marker-iso.sh,
// publishes into SHOAL_ISO_PUBLISH_DIR, and resolves profile iso_base → URL
// for Deploy Start when -iso-url is omitted.
// Phase 6a: InstallModeWrite writes /payload to a target with real SOL
// IMAGE_WRITE progress; optional dynamic BuildISO on Start.
//
// MVP still serves plain HTTP on the management segment (no TLS ISO server).
package iso
