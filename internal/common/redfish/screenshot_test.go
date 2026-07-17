package redfish

import (
	"context"
	"testing"
)

func TestDetectVendor(t *testing.T) {
	cases := []struct {
		in   []string
		want VendorID
	}{
		{[]string{"Dell Inc.", "PowerEdge R640"}, VendorDell},
		{[]string{"iDRAC", "Embedded"}, VendorDell},
		{[]string{"Supermicro", "X11DPi-NT"}, VendorSupermicro},
		{[]string{"Super Micro Computer", "BMC"}, VendorSupermicro},
		{[]string{"Shoal Virtual", "sushy"}, VendorUnknown},
	}
	for _, tc := range cases {
		got := detectVendor(tc.in...)
		if got != tc.want {
			t.Fatalf("%v: got %s want %s", tc.in, got, tc.want)
		}
	}
}

func TestSanitizePreviewRedactsSecrets(t *testing.T) {
	p := sanitizePreview(`{"Password":"x","foo":1}`)
	if p != "[redacted: body contained sensitive key]" {
		t.Fatalf("%q", p)
	}
}

func TestParseImagePayloadJPEGMagic(t *testing.T) {
	// minimal jpeg SOI
	b := []byte{0xff, 0xd8, 0xff, 0x00, 0x01}
	img, mt, err := parseImagePayload(b, "")
	if err != nil || mt != "image/jpeg" || len(img) != 5 {
		t.Fatalf("%v %s %v", err, mt, len(img))
	}
}

func TestFakeCaptureScreenshot(t *testing.T) {
	f := NewFake()
	ctx := context.TODO()
	_, err := f.CaptureScreenshot(ctx, "1", ScreenshotCurrent)
	if err == nil {
		t.Fatal("expected error without screenshot")
	}
	f.Screenshot = &Screenshot{Image: []byte{1, 2, 3}, MediaType: "image/png", Source: "test"}
	s, err := f.CaptureScreenshot(ctx, "1", ScreenshotCurrent)
	if err != nil || len(s.Image) != 3 {
		t.Fatalf("%v %+v", err, s)
	}
}
