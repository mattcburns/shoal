// Package discover owns hybrid asset ingestion (deterministic + AI + NetBox).
package discover

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/netbox"
	"github.com/mattcburns/shoal/internal/common/redact"
	"github.com/mattcburns/shoal/internal/common/secrets"
	"github.com/mattcburns/shoal/internal/common/validate"
	"github.com/mattcburns/shoal/internal/core/fewshot"
	"github.com/mattcburns/shoal/internal/core/reconcile"
	"github.com/mattcburns/shoal/internal/discover/adapters"
	"github.com/mattcburns/shoal/internal/discover/gate"
	"github.com/mattcburns/shoal/internal/discover/merge"
)

// IngestResult is returned to CLI/API callers.
type IngestResult struct {
	DeviceID    string                   `json:"device_id"`
	NetBoxID    string                   `json:"netbox_id,omitempty"`
	Asset       models.NormalizedAsset   `json:"asset"`
	Confidences []models.FieldConfidence `json:"confidences"`
	NeedsReview bool                     `json:"needs_review"`
	UsedAI      bool                     `json:"used_ai"`
}

// Service runs the hybrid pipeline.
type Service struct {
	Log        *slog.Logger
	Adapters   []adapters.Adapter
	Reconciler reconcile.Reconciler
	Secrets    secrets.Backend
	NetBox     netbox.API
	FewShot    fewshot.Store // optional; required for Confirm
}

// New constructs a Service with default adapters.
func New(log *slog.Logger, rec reconcile.Reconciler, sec secrets.Backend, nb netbox.API) *Service {
	return NewWithFewShot(log, rec, sec, nb, nil)
}

// NewWithFewShot constructs a Service with an optional learned few-shot store.
func NewWithFewShot(log *slog.Logger, rec reconcile.Reconciler, sec secrets.Backend, nb netbox.API, fs fewshot.Store) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		Log:        log,
		Adapters:   []adapters.Adapter{adapters.RedfishAdapter{}, adapters.CSVAdapter{}},
		Reconciler: rec,
		Secrets:    sec,
		NetBox:     nb,
		FewShot:    fs,
	}
}

// Ingest runs deterministic → gate → optional AI → merge → secrets → NetBox.
func (s *Service) Ingest(ctx context.Context, in models.RawAssetInput) (IngestResult, error) {
	if err := validate.RawAssetInput(in); err != nil {
		return IngestResult{}, err
	}
	kind := strings.ToLower(in.Kind)

	// Photo path: no deterministic adapter.
	if kind == "photo" {
		return s.ingestPhoto(ctx, in)
	}

	var partial adapters.Partial
	var parsed bool
	for _, a := range s.Adapters {
		if a.Kind() != kind {
			continue
		}
		p, err := a.Parse(in)
		if err != nil {
			return IngestResult{}, err
		}
		partial = p
		parsed = true
		break
	}
	if !parsed {
		return IngestResult{}, fmt.Errorf("discover: no adapter for kind %q", in.Kind)
	}

	usedAI := false
	var result models.NormalizationResult
	if gate.Accept(partial) {
		result = merge.Results(partial, nil)
		s.Log.Info("discover deterministic accept", "serial", result.Asset.Serial)
	} else {
		if s.Reconciler == nil {
			return IngestResult{}, fmt.Errorf("discover: AI required but reconciler not configured")
		}
		redacted := redact.Map(in.RedfishJSON)
		if kind == "csv" {
			// lift csv into map for redaction surface
			m := map[string]any{}
			for k, v := range in.CSVRow {
				m[k] = v
			}
			redacted = redact.Map(m)
		}
		aiRes, err := s.Reconciler.ReconcileAsset(ctx, reconcile.ReconcileAssetInput{
			RedactedRaw:    redacted,
			Partial:        &partial.Asset,
			PartialSources: partial.Confidences,
		})
		if err != nil {
			return IngestResult{}, err
		}
		usedAI = true
		result = merge.Results(partial, &aiRes)
		s.Log.Info("discover ai reconcile", "serial", result.Asset.Serial, "needs_review", result.NeedsReview)
	}

	return s.finalize(ctx, in, result, usedAI)
}

func (s *Service) ingestPhoto(ctx context.Context, in models.RawAssetInput) (IngestResult, error) {
	if s.Reconciler == nil {
		return IngestResult{}, fmt.Errorf("discover: photo path requires AI reconciler")
	}
	raw, err := base64.StdEncoding.DecodeString(in.PhotoBase64)
	if err != nil {
		// try raw base64 without padding issues
		raw, err = base64.RawStdEncoding.DecodeString(in.PhotoBase64)
		if err != nil {
			return IngestResult{}, fmt.Errorf("discover: photo_base64: %w", err)
		}
	}
	if len(raw) > 4<<20 {
		return IngestResult{}, fmt.Errorf("discover: photo exceeds 4 MiB")
	}
	media := "image/jpeg"
	if len(raw) >= 8 && string(raw[1:4]) == "PNG" {
		media = "image/png"
	}
	aiRes, err := s.Reconciler.ReconcileAssetPhoto(ctx, reconcile.ReconcilePhotoInput{
		Image:     raw,
		MediaType: media,
		BMCIP:     in.BMCIP,
	})
	if err != nil {
		return IngestResult{}, err
	}
	// no deterministic partial
	result := merge.Results(adapters.Partial{}, &aiRes)
	return s.finalize(ctx, in, result, true)
}

func (s *Service) finalize(ctx context.Context, in models.RawAssetInput, result models.NormalizationResult, usedAI bool) (IngestResult, error) {
	// Stash BMC credentials if provided.
	if in.BMCUsername != "" || in.BMCPassword != "" {
		if s.Secrets == nil {
			return IngestResult{}, fmt.Errorf("discover: secrets backend required for credentials")
		}
		ref := "bmc-" + sanitizeRef(result.Asset.Serial)
		if result.Asset.Serial == "" {
			ref = "bmc-unknown"
		}
		if err := s.Secrets.Put(ctx, ref, secrets.Credential{
			Username: in.BMCUsername,
			Password: in.BMCPassword,
		}); err != nil {
			return IngestResult{}, err
		}
		result.Asset.CredentialRef = ref
	}
	if result.Asset.BMCIP == "" && in.BMCIP != "" {
		result.Asset.BMCIP = in.BMCIP
	}
	if err := validate.NormalizationResult(result); err != nil {
		return IngestResult{}, err
	}

	deviceID := result.Asset.Serial
	netboxID := ""
	if s.NetBox != nil {
		id, err := s.NetBox.UpsertDevice(ctx, models.DeviceIdentity{
			Name:           result.Asset.Serial,
			Serial:         result.Asset.Serial,
			LifecycleState: models.StateDiscovered,
			CredentialRef:  result.Asset.CredentialRef,
			BMCIP:          result.Asset.BMCIP,
		})
		if err != nil {
			return IngestResult{}, fmt.Errorf("discover: netbox: %w", err)
		}
		netboxID = id
		deviceID = id
	}

	return IngestResult{
		DeviceID:    deviceID,
		NetBoxID:    netboxID,
		Asset:       result.Asset,
		Confidences: result.Confidences,
		NeedsReview: result.NeedsReview,
		UsedAI:      usedAI,
	}, nil
}

func sanitizeRef(s string) string {
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
