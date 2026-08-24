package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mattcburns/shoal/internal/api"
	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/netbox"
	"github.com/mattcburns/shoal/internal/common/secrets"
)

type deviceNB interface {
	GetDevice(ctx context.Context, key string) (models.DeviceIdentity, error)
	SetCredentialRef(ctx context.Context, deviceKey, ref, bmcIP string) error
}

type deviceCreds struct {
	secrets secrets.Backend
	nb      deviceNB
}

func (d deviceCreds) Get(ctx context.Context, deviceID, credentialRef string) (api.DeviceCredentialsView, error) {
	var id models.DeviceIdentity
	if ref := strings.TrimSpace(credentialRef); ref != "" {
		id.ID = deviceID
		id.CredentialRef = ref
	} else {
		got, err := d.lookup(ctx, deviceID)
		if err != nil {
			if !isNotFound(err) {
				return api.DeviceCredentialsView{}, err
			}
		} else {
			id = got
		}
	}
	ref := credRefFor(id, deviceID)
	id.CredentialRef = ref
	var cred secrets.Credential
	if d.secrets != nil && ref != "" {
		got, err := d.secrets.Get(ctx, ref)
		if err != nil && !errors.Is(err, secrets.ErrNotFound) {
			return api.DeviceCredentialsView{}, err
		}
		if err == nil {
			cred = got
		}
	}
	return viewFrom(deviceID, id, cred), nil
}

func (d deviceCreds) Put(ctx context.Context, deviceID string, req api.DeviceCredentialsPut) (api.DeviceCredentialsView, error) {
	if d.secrets == nil {
		return api.DeviceCredentialsView{}, fmt.Errorf("secrets backend not configured (set SHOAL_SECRETS_DIR)")
	}
	id, err := d.lookup(ctx, deviceID)
	if err != nil {
		if isNotFound(err) {
			return api.DeviceCredentialsView{}, fmt.Errorf("device %q not found", deviceID)
		}
		return api.DeviceCredentialsView{}, err
	}
	if d.nb != nil && strings.TrimSpace(id.Serial) == "" && strings.TrimSpace(id.ID) == "" {
		return api.DeviceCredentialsView{}, fmt.Errorf("device %q not found", deviceID)
	}
	existing, _ := d.secrets.Get(ctx, credRefFor(id, deviceID))
	user := strings.TrimSpace(req.Username)
	if user == "" {
		user = existing.Username
	}
	if user == "" {
		return api.DeviceCredentialsView{}, fmt.Errorf("username is required")
	}
	pass := req.Password
	if pass == "" {
		pass = existing.Password
	}
	if pass == "" {
		return api.DeviceCredentialsView{}, fmt.Errorf("password is required for new credentials")
	}
	ref := credRefFor(id, deviceID)
	if err := d.secrets.Put(ctx, ref, secrets.Credential{Username: user, Password: pass}); err != nil {
		return api.DeviceCredentialsView{}, err
	}
	id.CredentialRef = ref
	if ip := strings.TrimSpace(req.BMCIP); ip != "" {
		id.BMCIP = ip
	}
	if d.nb != nil {
		if err := d.nb.SetCredentialRef(ctx, deviceID, ref, strings.TrimSpace(req.BMCIP)); err != nil {
			return api.DeviceCredentialsView{}, fmt.Errorf("netbox: %w", err)
		}
	}
	cred, err := d.secrets.Get(ctx, ref)
	if err != nil {
		return api.DeviceCredentialsView{}, err
	}
	return viewFrom(deviceID, id, cred), nil
}

func (d deviceCreds) Resolve(ctx context.Context, deviceID string) (username, password string, err error) {
	view, err := d.Get(ctx, deviceID, "")
	if err != nil {
		return "", "", err
	}
	if d.secrets == nil || view.CredentialRef == "" {
		return "", "", secrets.ErrNotFound
	}
	cred, err := d.secrets.Get(ctx, view.CredentialRef)
	if err != nil {
		return "", "", err
	}
	if cred.Username == "" && cred.Password == "" {
		return "", "", secrets.ErrNotFound
	}
	return cred.Username, cred.Password, nil
}

func (d deviceCreds) lookup(ctx context.Context, deviceID string) (models.DeviceIdentity, error) {
	if d.nb == nil {
		return models.DeviceIdentity{}, nil
	}
	return d.nb.GetDevice(ctx, deviceID)
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") || strings.Contains(msg, "status 404")
}

func credRefFor(id models.DeviceIdentity, deviceID string) string {
	if r := strings.TrimSpace(id.CredentialRef); r != "" {
		return r
	}
	serial := strings.TrimSpace(id.Serial)
	if serial == "" {
		serial = strings.TrimSpace(deviceID)
	}
	return "bmc-" + sanitizeCredRef(serial)
}

func viewFrom(deviceID string, id models.DeviceIdentity, cred secrets.Credential) api.DeviceCredentialsView {
	did := strings.TrimSpace(id.ID)
	if did == "" {
		did = deviceID
	}
	return api.DeviceCredentialsView{
		DeviceID:      did,
		CredentialRef: id.CredentialRef,
		Username:      cred.Username,
		HasPassword:   cred.Password != "",
		BMCIP:         id.BMCIP,
	}
}

func sanitizeCredRef(s string) string {
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, s)
	if s == "" {
		return "x"
	}
	return s
}

var _ api.DeviceCredentials = deviceCreds{}
var _ deviceNB = (*netbox.Client)(nil)
var _ deviceNB = (*netbox.Memory)(nil)
