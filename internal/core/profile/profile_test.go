package profile_test

import (
	"context"
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/core/ai"
	"github.com/mattcburns/shoal/internal/core/profile"
)

func TestGenerateProvisioningProfile(t *testing.T) {
	fake := &ai.Fake{Content: `{
  "ref": "lab-1-ubuntu",
  "iso_base": "ubuntu-22.04-live",
  "post_install_steps": ["set hostname lab-1"],
  "destruct_steps": [],
  "needs_approval": false
}`}
	svc, err := profile.New(fake, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.GenerateProvisioningProfile(context.Background(),
		models.NormalizedAsset{Serial: "S1", BMCIP: "10.0.0.1", Vendor: "V", Model: "M"},
		models.ProfileRequirements{OSFamily: "ubuntu", Hostname: "lab-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if p.Ref != "lab-1-ubuntu" || p.ISOBase == "" {
		t.Fatalf("%+v", p)
	}
}

func TestGenerateForcesApprovalOnDestruct(t *testing.T) {
	fake := &ai.Fake{Content: `{
  "ref": "wipe",
  "iso_base": "ubuntu-22.04-live",
  "destruct_steps": ["wipe disk"],
  "needs_approval": false
}`}
	svc, err := profile.New(fake, nil)
	if err != nil {
		t.Fatal(err)
	}
	// allow_destruct true so steps kept; generator must force needs_approval
	p, err := svc.GenerateProvisioningProfile(context.Background(),
		models.NormalizedAsset{Serial: "S2", BMCIP: "10.0.0.2"},
		models.ProfileRequirements{OSFamily: "ubuntu", AllowDestruct: true},
	)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !p.NeedsApproval || len(p.DestructSteps) == 0 {
		t.Fatalf("expected forced approval + destruct steps, got %+v", p)
	}
}

func TestGenerateStripsDestructWhenNotAllowed(t *testing.T) {
	fake := &ai.Fake{Content: `{
  "ref": "safe",
  "iso_base": "ubuntu-22.04-live",
  "destruct_steps": ["wipe disk"],
  "needs_approval": true
}`}
	svc, err := profile.New(fake, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.GenerateProvisioningProfile(context.Background(),
		models.NormalizedAsset{Serial: "S3", BMCIP: "10.0.0.3"},
		models.ProfileRequirements{OSFamily: "ubuntu", AllowDestruct: false},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.DestructSteps) != 0 {
		t.Fatalf("destruct should be stripped: %+v", p)
	}
}

func TestFileStoreApprove(t *testing.T) {
	dir := t.TempDir()
	st, err := profile.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rec, err := st.Save(ctx, models.ProvisioningProfile{
		Ref: "p1", ISOBase: "iso", NeedsApproval: true,
		DestructSteps: []string{"wipe"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rec.NeedsOperatorApproval() {
		t.Fatal("expected needs approval")
	}
	rec, err = st.Approve(ctx, "p1", "matt")
	if err != nil {
		t.Fatal(err)
	}
	if rec.NeedsOperatorApproval() || rec.ApprovedBy != "matt" {
		t.Fatalf("%+v", rec)
	}
}
