package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/mattcburns/shoal/internal/api"
	"github.com/mattcburns/shoal/internal/common/config"
	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/netbox"
	"github.com/mattcburns/shoal/internal/common/secrets"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
	"github.com/mattcburns/shoal/internal/observe/poll"
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

// TestSeedPollTargetsUsesJobCredentialRef pins the fix for a real-hardware bug
// found 2026-08-26: the background poller was seeded with the global
// SHOAL_BMC_* lab defaults for every device, ignoring each job's own
// CredentialRef. Against a real BMC whose stored credential differs from the
// lab default (this device: root/realpass vs. the lab's admin/password), that
// meant every poll cycle authenticated wrong. iDRACs rate-limit and briefly
// block the source IP after repeated failed logins, so this was a
// self-inflicted denial of service against the same BMC an active job needed
// to reach -- misdiagnosed for a full session as unexplained "iDRAC
// flakiness" before the credential mismatch was found in the iDRAC's own
// login-attempt log.
func TestSeedPollTargetsUsesJobCredentialRef(t *testing.T) {
	ctx := context.Background()
	sec := secrets.NewMemory()
	if err := sec.Put(ctx, "bmc-real", secrets.Credential{Username: "root", Password: "realpass"}); err != nil {
		t.Fatal(err)
	}
	store := jobstore.NewMemory()
	if err := store.Insert(ctx, models.Job{
		ID: "job-real", DeviceID: "6", State: models.StateProvisioning,
		BMCEndpoint: "https://172.16.21.202", CredentialRef: "bmc-real",
	}); err != nil {
		t.Fatal(err)
	}
	// No CredentialRef -- must still fall back to the global lab default.
	if err := store.Insert(ctx, models.Job{
		ID: "job-lab", DeviceID: "1", State: models.StateProvisioning,
		BMCEndpoint: "http://127.0.0.1:8001",
	}); err != nil {
		t.Fatal(err)
	}

	p := poll.New(nil, nil, nil)
	cfg := config.Config{BMCUsername: "admin", BMCPassword: "password"}
	seedPollTargets(ctx, p, store, cfg, sec)

	byDevice := map[string]poll.Target{}
	for _, tgt := range p.Targets() {
		byDevice[tgt.DeviceID] = tgt
	}
	real, ok := byDevice["6"]
	if !ok {
		t.Fatal("device 6 not seeded")
	}
	if real.BMC.Username != "root" || real.BMC.Password != "realpass" {
		t.Fatalf("real device got wrong creds: %+v", real.BMC)
	}
	lab, ok := byDevice["1"]
	if !ok {
		t.Fatal("device 1 not seeded")
	}
	if lab.BMC.Username != "admin" || lab.BMC.Password != "password" {
		t.Fatalf("no-CredentialRef device should fall back to global default: %+v", lab.BMC)
	}
}
