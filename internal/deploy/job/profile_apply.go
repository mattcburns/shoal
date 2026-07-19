package job

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/deploy/iso"
)

// applyProfileDefaults fills empty StartJobRequest fields from a provisioning profile.
// Non-empty request fields always win (operator one-off overrides).
func applyProfileDefaults(req *models.StartJobRequest, p models.ProvisioningProfile) {
	if req == nil {
		return
	}
	if strings.TrimSpace(req.InstallStrategy) == "" && strings.TrimSpace(p.InstallStrategy) != "" {
		req.InstallStrategy = strings.TrimSpace(p.InstallStrategy)
	}
	if strings.TrimSpace(req.OsFamily) == "" && strings.TrimSpace(p.OSFamily) != "" {
		req.OsFamily = strings.TrimSpace(p.OSFamily)
	}
	if strings.TrimSpace(req.SeedDelivery) == "" && strings.TrimSpace(p.SeedDelivery) != "" {
		req.SeedDelivery = strings.TrimSpace(p.SeedDelivery)
	}
	if strings.TrimSpace(req.SeedISOURL) == "" && strings.TrimSpace(p.SeedISOURL) != "" {
		req.SeedISOURL = strings.TrimSpace(p.SeedISOURL)
	}
	if strings.TrimSpace(req.Prep) == "" && strings.TrimSpace(p.Prep) != "" {
		req.Prep = strings.TrimSpace(p.Prep)
	}
	if strings.TrimSpace(req.WipeLevel) == "" && strings.TrimSpace(p.WipeLevel) != "" {
		req.WipeLevel = strings.TrimSpace(p.WipeLevel)
	}
	if strings.TrimSpace(req.PrepISOURL) == "" && strings.TrimSpace(p.PrepISOURL) != "" {
		req.PrepISOURL = strings.TrimSpace(p.PrepISOURL)
	}
	if strings.TrimSpace(req.ISOHostname) == "" && strings.TrimSpace(p.ISOHostname) != "" {
		req.ISOHostname = strings.TrimSpace(p.ISOHostname)
	}
	// media_url → iso_url when request omitted install media
	if strings.TrimSpace(req.ISOURL) == "" && strings.TrimSpace(p.MediaURL) != "" {
		req.ISOURL = strings.TrimSpace(p.MediaURL)
	}
	if req.StageTimeout <= 0 && strings.TrimSpace(p.StageTimeout) != "" {
		if d, err := time.ParseDuration(strings.TrimSpace(p.StageTimeout)); err == nil && d > 0 {
			req.StageTimeout = d
		}
	}
}

// resolveProfileURLs fills iso_url / seed_iso_url / prep_iso_url from profile bases
// when still empty after applyProfileDefaults.
func resolveProfileURLs(req *models.StartJobRequest, p models.ProvisioningProfile, isoBaseURL string) error {
	if req == nil {
		return nil
	}
	if strings.TrimSpace(req.ISOURL) == "" {
		base := strings.TrimSpace(p.ISOBase)
		if base != "" {
			u, err := iso.ResolveFromProfile(base, isoBaseURL)
			if err != nil {
				return fmt.Errorf("job: resolve iso from profile: %w", err)
			}
			req.ISOURL = u
		}
	}
	if strings.TrimSpace(req.SeedISOURL) == "" {
		if u := strings.TrimSpace(p.SeedISOURL); u != "" {
			req.SeedISOURL = u
		} else if base := strings.TrimSpace(p.SeedISOBase); base != "" {
			u, err := iso.ResolveFromProfile(base, isoBaseURL)
			if err != nil {
				return fmt.Errorf("job: resolve seed iso from profile: %w", err)
			}
			req.SeedISOURL = u
		}
	}
	if strings.TrimSpace(req.PrepISOURL) == "" {
		if u := strings.TrimSpace(p.PrepISOURL); u != "" {
			req.PrepISOURL = u
		} else if base := strings.TrimSpace(p.PrepISOBase); base != "" {
			u, err := iso.ResolveFromProfile(base, isoBaseURL)
			if err != nil {
				return fmt.Errorf("job: resolve prep iso from profile: %w", err)
			}
			req.PrepISOURL = u
		}
	}
	return nil
}
