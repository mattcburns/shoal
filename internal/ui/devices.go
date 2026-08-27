package ui

import (
	"errors"
	"net/http"
	"strings"

	"github.com/mattcburns/shoal/internal/common/directory"
	"github.com/mattcburns/shoal/internal/common/models"
)

// deviceListData is the devices_list.html template's data.
type deviceListData struct {
	Devices []models.DeviceIdentity
	Error   string
}

// deviceFormData is the device_form.html template's data (shared by
// GET/POST /ui/devices/new and GET/POST /ui/devices/{id}/edit). CSRFToken is
// echoed back into a hidden field on both forms the page renders (save, and
// -- when Editing -- delete); see auth.go's csrfToken/verifyCSRF.
type deviceFormData struct {
	Editing   bool
	Action    string
	Device    models.DeviceIdentity
	Error     string
	CSRFToken string
}

func (s *Server) handleDeviceList(w http.ResponseWriter, r *http.Request) {
	if s.Directory == nil {
		s.renderPage(w, r, "devices_list.html", deviceListData{Error: "device directory not configured"})
		return
	}
	devices, err := s.Directory.ListDevices(r.Context())
	if err != nil {
		s.log.Error("ui: list devices", "err", err.Error())
		s.renderPage(w, r, "devices_list.html", deviceListData{Error: "failed to load devices"})
		return
	}
	s.renderPage(w, r, "devices_list.html", deviceListData{Devices: devices})
}

func (s *Server) handleDeviceNewForm(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, r, "device_form.html", deviceFormData{
		Editing:   false,
		Action:    "/ui/devices/new",
		CSRFToken: s.csrfToken(r),
	})
}

func (s *Server) handleDeviceNewSubmit(w http.ResponseWriter, r *http.Request) {
	if s.Directory == nil {
		s.renderPage(w, r, "device_form.html", deviceFormData{
			Action:    "/ui/devices/new",
			Error:     "device directory not configured",
			Device:    deviceFromForm(r, models.DeviceIdentity{}),
			CSRFToken: s.csrfToken(r),
		})
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderPage(w, r, "device_form.html", deviceFormData{
			Action:    "/ui/devices/new",
			Error:     "invalid form submission",
			CSRFToken: s.csrfToken(r),
		})
		return
	}
	if !s.verifyCSRF(r) {
		s.renderPage(w, r, "device_form.html", deviceFormData{
			Action:    "/ui/devices/new",
			Error:     "your session expired; please retry",
			CSRFToken: s.csrfToken(r),
		})
		return
	}
	d := deviceFromForm(r, models.DeviceIdentity{})
	if strings.TrimSpace(d.Name) == "" {
		s.renderPage(w, r, "device_form.html", deviceFormData{
			Action:    "/ui/devices/new",
			Error:     "name is required",
			Device:    d,
			CSRFToken: s.csrfToken(r),
		})
		return
	}
	// Matches POST /v1/devices' handleCreateDevice: lifecycle_state is never
	// client-settable on create, every new device starts at discovered.
	d.LifecycleState = models.StateDiscovered
	id, err := s.Directory.UpsertDevice(r.Context(), d)
	if err != nil {
		s.log.Error("ui: create device", "err", err.Error())
		s.renderPage(w, r, "device_form.html", deviceFormData{
			Action:    "/ui/devices/new",
			Error:     "failed to save device",
			Device:    d,
			CSRFToken: s.csrfToken(r),
		})
		return
	}
	http.Redirect(w, r, "/ui/devices/"+id, http.StatusFound)
}

func (s *Server) handleDeviceEditForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, ok := s.loadDevice(w, r, id)
	if !ok {
		return
	}
	s.renderPage(w, r, "device_form.html", deviceFormData{
		Editing:   true,
		Action:    "/ui/devices/" + id + "/edit",
		Device:    d,
		CSRFToken: s.csrfToken(r),
	})
}

func (s *Server) handleDeviceEditSubmit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.Directory == nil {
		http.Error(w, "device directory not configured", http.StatusServiceUnavailable)
		return
	}
	existing, ok := s.loadDevice(w, r, id)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderPage(w, r, "device_form.html", deviceFormData{
			Editing:   true,
			Action:    "/ui/devices/" + id + "/edit",
			Error:     "invalid form submission",
			Device:    existing,
			CSRFToken: s.csrfToken(r),
		})
		return
	}
	if !s.verifyCSRF(r) {
		s.renderPage(w, r, "device_form.html", deviceFormData{
			Editing:   true,
			Action:    "/ui/devices/" + id + "/edit",
			Error:     "your session expired; please retry",
			Device:    existing,
			CSRFToken: s.csrfToken(r),
		})
		return
	}
	d := deviceFromForm(r, existing)
	d.ID = id
	if strings.TrimSpace(d.Name) == "" {
		s.renderPage(w, r, "device_form.html", deviceFormData{
			Editing:   true,
			Action:    "/ui/devices/" + id + "/edit",
			Error:     "name is required",
			Device:    d,
			CSRFToken: s.csrfToken(r),
		})
		return
	}
	if _, err := s.Directory.UpsertDevice(r.Context(), d); err != nil {
		s.log.Error("ui: update device", "device_id", id, "err", err.Error())
		s.renderPage(w, r, "device_form.html", deviceFormData{
			Editing:   true,
			Action:    "/ui/devices/" + id + "/edit",
			Error:     "failed to save device",
			Device:    d,
			CSRFToken: s.csrfToken(r),
		})
		return
	}
	http.Redirect(w, r, "/ui/devices/"+id, http.StatusFound)
}

func (s *Server) handleDeviceDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.Directory == nil {
		http.Error(w, "device directory not configured", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form submission", http.StatusBadRequest)
		return
	}
	if !s.verifyCSRF(r) {
		http.Error(w, "invalid or expired form token", http.StatusForbidden)
		return
	}
	if err := s.Directory.DeleteDevice(r.Context(), id); err != nil && !errors.Is(err, directory.ErrNotFound) {
		s.log.Error("ui: delete device", "device_id", id, "err", err.Error())
		http.Error(w, "failed to delete device", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/ui/devices", http.StatusFound)
}

// loadDevice fetches a device by path id, writing a 404/503 response and
// returning ok=false when it can't. Callers must return immediately on !ok.
func (s *Server) loadDevice(w http.ResponseWriter, r *http.Request, id string) (models.DeviceIdentity, bool) {
	if s.Directory == nil {
		http.Error(w, "device directory not configured", http.StatusServiceUnavailable)
		return models.DeviceIdentity{}, false
	}
	if strings.TrimSpace(id) == "" {
		http.NotFound(w, r)
		return models.DeviceIdentity{}, false
	}
	d, err := s.Directory.GetDevice(r.Context(), id)
	if err != nil {
		if errors.Is(err, directory.ErrNotFound) {
			http.NotFound(w, r)
			return models.DeviceIdentity{}, false
		}
		s.log.Error("ui: get device", "device_id", id, "err", err.Error())
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return models.DeviceIdentity{}, false
	}
	return d, true
}

// deviceFromForm overlays POSTed form fields onto base (base carries ID and
// any existing values on an edit; a create passes the zero value). Only the
// fields the Add/Edit Device form exposes are read.
func deviceFromForm(r *http.Request, base models.DeviceIdentity) models.DeviceIdentity {
	d := base
	d.Name = strings.TrimSpace(r.FormValue("name"))
	d.Serial = strings.TrimSpace(r.FormValue("serial"))
	d.Vendor = strings.TrimSpace(r.FormValue("vendor"))
	d.Model = strings.TrimSpace(r.FormValue("model"))
	d.BMCIP = strings.TrimSpace(r.FormValue("bmc_ip"))
	d.CredentialRef = strings.TrimSpace(r.FormValue("credential_ref"))
	return d
}
