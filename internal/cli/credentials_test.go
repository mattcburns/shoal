package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/mattcburns/shoal/internal/api"
	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/netbox"
	"github.com/mattcburns/shoal/internal/common/secrets"
)

func TestDeviceCredsPutGetNoPasswordLeak(t *testing.T) {
	nb := netbox.NewMemory()
	_, err := nb.UpsertDevice(context.Background(), models.DeviceIdentity{
		Name: "C784MH3", Serial: "C784MH3", BMCIP: "172.16.21.202",
	})
	if err != nil {
		t.Fatal(err)
	}
	d := deviceCreds{secrets: secrets.NewMemory(), nb: nb}
	view, err := d.Put(context.Background(), "C784MH3", api.DeviceCredentialsPut{
		Username: "root", Password: "s3cret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Username != "root" || !view.HasPassword || view.CredentialRef != "bmc-C784MH3" {
		t.Fatalf("%+v", view)
	}
	got, err := d.Get(context.Background(), "C784MH3", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "root" || !got.HasPassword {
		t.Fatalf("%+v", got)
	}
	u, p, err := d.Resolve(context.Background(), "C784MH3")
	if err != nil || u != "root" || p != "s3cret" {
		t.Fatalf("resolve %q %q %v", u, p, err)
	}
	dev, _ := nb.GetDevice(context.Background(), "C784MH3")
	if dev.CredentialRef != "bmc-C784MH3" {
		t.Fatalf("netbox ref %q", dev.CredentialRef)
	}
}

func TestDeviceCredsKeepPasswordOnUsernameUpdate(t *testing.T) {
	d := deviceCreds{secrets: secrets.NewMemory()}
	if _, err := d.Put(context.Background(), "6", api.DeviceCredentialsPut{Username: "root", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	got, err := d.Put(context.Background(), "6", api.DeviceCredentialsPut{Username: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "admin" || !got.HasPassword {
		t.Fatalf("%+v", got)
	}
	_, p, err := d.Resolve(context.Background(), "6")
	if err != nil || p != "pw" {
		t.Fatalf("password not kept: %q %v", p, err)
	}
}

func TestDeviceCredsPutByNetBoxIDDoesNotWipeClassification(t *testing.T) {
	nb := netbox.NewMemory()
	id, err := nb.UpsertDevice(context.Background(), models.DeviceIdentity{
		Name: "C784MH3", Serial: "C784MH3", BMCIP: "172.16.21.202",
		Vendor: "Dell Inc.", Model: "PowerEdge R750",
	})
	if err != nil {
		t.Fatal(err)
	}
	d := deviceCreds{secrets: secrets.NewMemory(), nb: nb}
	view, err := d.Put(context.Background(), id, api.DeviceCredentialsPut{Username: "root", Password: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	if view.CredentialRef != "bmc-C784MH3" || view.Username != "root" {
		t.Fatalf("%+v", view)
	}
	dev, err := nb.GetDevice(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if dev.Vendor != "Dell Inc." || dev.Model != "PowerEdge R750" {
		t.Fatalf("classification wiped: %+v", dev)
	}
	if dev.CredentialRef != "bmc-C784MH3" || dev.BMCIP != "172.16.21.202" {
		t.Fatalf("cf %+v", dev)
	}
}

func TestDeviceCredsGetWithHintSkipsNetBox(t *testing.T) {
	nb := netbox.NewMemory()
	sec := secrets.NewMemory()
	d := deviceCreds{secrets: sec, nb: nb}
	if err := sec.Put(context.Background(), "bmc-C784MH3", secrets.Credential{Username: "root", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	got, err := d.Get(context.Background(), "6", "bmc-C784MH3")
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "root" || !got.HasPassword || got.CredentialRef != "bmc-C784MH3" {
		t.Fatalf("%+v", got)
	}
}

func TestDeviceCredsGetEmptyWhenUnknown(t *testing.T) {
	d := deviceCreds{secrets: secrets.NewMemory(), nb: netbox.NewMemory()}
	got, err := d.Get(context.Background(), "6", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.HasPassword || got.Username != "" {
		t.Fatalf("%+v", got)
	}
}

func TestDeviceCredsPutUnknownDevice(t *testing.T) {
	d := deviceCreds{secrets: secrets.NewMemory(), nb: netbox.NewMemory()}
	_, err := d.Put(context.Background(), "6", api.DeviceCredentialsPut{Username: "root", Password: "x"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err %v", err)
	}
}

func TestDeviceCredsPutRequiresPasswordForNew(t *testing.T) {
	d := deviceCreds{secrets: secrets.NewMemory()}
	_, err := d.Put(context.Background(), "6", api.DeviceCredentialsPut{Username: "root"})
	if err == nil || !strings.Contains(err.Error(), "password is required") {
		t.Fatalf("err %v", err)
	}
}
