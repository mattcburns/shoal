package reconcile

import "testing"

func TestParseOCRIdentityDeepseekStyle(t *testing.T) {
	ocr := parseOCRIdentity("SERIAL: LAB-P3-PHOTO-001\n\nVENDOR Shoal Virtual\n\nMODEL: test-node")
	if ocr.Serial != "LAB-P3-PHOTO-001" {
		t.Fatalf("serial %q", ocr.Serial)
	}
	if ocr.Vendor != "Shoal Virtual" {
		t.Fatalf("vendor %q", ocr.Vendor)
	}
	if ocr.Model != "test-node" {
		t.Fatalf("model %q", ocr.Model)
	}
	r, err := resultFromOCR(ocr, "10.77.77.77")
	if err != nil {
		t.Fatal(err)
	}
	if r.Asset.BMCIP != "10.77.77.77" || r.Asset.Serial != "LAB-P3-PHOTO-001" {
		t.Fatalf("%+v", r.Asset)
	}
	if r.NeedsReview {
		t.Fatal("full OCR should not need review")
	}
}

func TestResultFromOCRRequiresSerial(t *testing.T) {
	_, err := resultFromOCR(ocrFields{RawText: "a blurry photo of a rack"}, "1.2.3.4")
	if err == nil {
		t.Fatal("expected error")
	}
}
