// Package iso builds and publishes bootable images for BMC Virtual Media.
//
// Builder wraps the external build-marker-iso.sh script, publishes the
// result into SHOAL_ISO_PUBLISH_DIR, and resolves a profile's iso_base to
// a servable URL when Deploy Start omits -iso-url. InstallModeWrite instead
// writes /payload to the target directly, reporting progress over SOL as
// IMAGE_WRITE events. Images are served over plain HTTP on the management
// segment; there is no TLS ISO server.
package iso
