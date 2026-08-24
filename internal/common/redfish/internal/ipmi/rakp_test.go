package ipmi

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	s = compactHex(s)
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func compactHex(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != ' ' && c != '\n' && c != '\t' {
			out = append(out, c)
		}
	}
	return string(out)
}

var fixture = struct {
	password     string
	sidc         uint32
	sidm         uint32
	rc, rm, guid []byte
	role         byte
	user         []byte
}{
	password: "TestPass",
	sidc:     0xA0A2A3A4,
	sidm:     0x01234567,
	role:     roleAdminNameOnly,
	user:     []byte("admin"),
}

func initFixture(t *testing.T) {
	t.Helper()
	fixture.rc = mustHex(t, "01 02 03 04 05 06 07 08 09 0A 0B 0C 0D 0E 0F 10")
	fixture.rm = mustHex(t, "11 12 13 14 15 16 17 18 19 1A 1B 1C 1D 1E 1F 20")
	fixture.guid = mustHex(t, "A1 A2 A3 A4 A5 A6 A7 A8 A9 AA AB AC AD AE AF B0")
}

func TestRAKPVectors_Suite3_SpecConcat(t *testing.T) {
	initFixture(t)
	f := fixture
	ulen := byte(len(f.user))
	keys := deriveKeys(suite3, f.password, f.rc, f.rm, f.role, ulen, f.user)

	wantKUID := mustHex(t, "54 65 73 74 50 61 73 73 00 00 00 00 00 00 00 00 00 00 00 00")
	if !bytes.Equal(keys.kuid, wantKUID) {
		t.Fatalf("K_UID=\n%s\nwant\n%s", hex.Dump(keys.kuid), hex.Dump(wantKUID))
	}

	kecc2 := keys.kecc2(f.sidc, f.sidm, f.rc, f.rm, f.guid, f.role, ulen, f.user)
	want := map[string][]byte{
		"KECC2": kecc2,
		"SIK":   keys.sik,
		"K1":    keys.k1,
		"K2":    keys.k2,
		"KECC3": keys.kecc3(f.rm, f.sidc, f.role, ulen, f.user),
		"ICV":   keys.icv(f.rm, f.sidc, f.guid),
	}
	expect := map[string][]byte{
		"KECC2": mustHex(t, "54 4B 64 66 E8 50 C4 58 68 36 4B 78 D5 54 49 94 DA 87 F2 6F"),
		"SIK":   mustHex(t, "3B 19 7D 0D 52 54 32 0A 4C 95 41 A4 D0 FD FF 96 DC FB A4 66"),
		"K1":    mustHex(t, "A5 21 F2 FD 18 6A 52 0B 69 BB BE 4A BF 08 88 D7 07 CA 1B D5"),
		"K2":    mustHex(t, "37 B1 6B 63 17 04 EB 2E 8F 07 DC E9 0A D8 33 26 33 0F 02 1F"),
		"KECC3": mustHex(t, "3E FC 2F F3 C6 0F E8 D1 BF 8D BC E2 A8 89 BA 13 4B BC 1B 12"),
		"ICV":   mustHex(t, "11 B3 EF E0 68 BA 03 20 21 C7 CE 98"),
	}
	for name, got := range want {
		if !bytes.Equal(got, expect[name]) {
			t.Errorf("%s=\n%s\nwant\n%s", name, hex.Dump(got), hex.Dump(expect[name]))
		}
	}

	wire := packRAKP1(0, f.sidm, f.rc, f.role, ulen, f.user)
	wantWire := mustHex(t, "00 00 00 00 67 45 23 01 01 02 03 04 05 06 07 08 09 0A 0B 0C 0D 0E 0F 10 14 00 00 05 61 64 6D 69 6E")
	if !bytes.Equal(wire, wantWire) {
		t.Fatalf("RAKP1 wire=\n%s\nwant\n%s", hex.Dump(wire), hex.Dump(wantWire))
	}
	if wire[24] != 0x14 || wire[27] != 0x05 {
		t.Fatalf("RAKP1 role/ulen offsets: [24]=%02x [27]=%02x", wire[24], wire[27])
	}
}

func TestRAKPVectors_Suite17_SpecConcat(t *testing.T) {
	initFixture(t)
	f := fixture
	ulen := byte(len(f.user))
	keys := deriveKeys(suite17, f.password, f.rc, f.rm, f.role, ulen, f.user)

	want := map[string][]byte{
		"KECC2": keys.kecc2(f.sidc, f.sidm, f.rc, f.rm, f.guid, f.role, ulen, f.user),
		"SIK":   keys.sik,
		"K1":    keys.k1,
		"K2":    keys.k2,
		"KECC3": keys.kecc3(f.rm, f.sidc, f.role, ulen, f.user),
		"ICV":   keys.icv(f.rm, f.sidc, f.guid),
	}
	expect := map[string][]byte{
		"KECC2": mustHex(t, "3E 4F EE 78 D3 3E F5 CB DA 8A C4 7C 22 24 30 05 20 A4 6D 0B C0 F1 C8 F2 4A 6D 18 0E E5 D7 9D 6B"),
		"SIK":   mustHex(t, "A6 5F 45 EC 03 B1 5E 8F FC 02 A1 B6 96 5B 35 D5 25 0C A3 F5 03 F3 87 1D 4C 87 07 52 D8 94 25 7C"),
		"K1":    mustHex(t, "04 DB 8E 56 06 A1 85 A5 94 56 BD 35 42 9C 1C 0D 28 40 34 6F A9 2B ED 0E 29 7E D8 02 45 F3 E9 0A"),
		"K2":    mustHex(t, "5A 1E 6F C5 7F E6 39 71 95 B8 51 20 75 96 A8 7E 2D 59 16 AE 4A 0D 37 66 44 76 CF F6 63 B6 94 8B"),
		"KECC3": mustHex(t, "AC 03 00 4E 0B CF F6 CF D6 2A CC EE 91 15 4E E1 22 83 16 37 D2 53 7D B6 8D C5 FA 51 C4 2C 78 96"),
		"ICV":   mustHex(t, "78 85 08 20 28 E4 00 E3 CF 5B 93 F2 6A 99 76 4E"),
	}
	for name, got := range want {
		if !bytes.Equal(got, expect[name]) {
			t.Errorf("%s=\n%s\nwant\n%s", name, hex.Dump(got), hex.Dump(expect[name]))
		}
	}
	aes := keys.aesKey()
	wantAES := mustHex(t, "5A 1E 6F C5 7F E6 39 71 95 B8 51 20 75 96 A8 7E")
	if !bytes.Equal(aes, wantAES) {
		t.Fatalf("AES key=\n%s\nwant\n%s", hex.Dump(aes), hex.Dump(wantAES))
	}
	wire := packRAKP1(0, f.sidm, f.rc, f.role, ulen, f.user)
	wantWire := mustHex(t, "00 00 00 00 67 45 23 01 01 02 03 04 05 06 07 08 09 0A 0B 0C 0D 0E 0F 10 14 00 00 05 61 64 6D 69 6E")
	if !bytes.Equal(wire, wantWire) {
		t.Fatalf("RAKP1 wire (suite 17 same pack)=\n%s\nwant\n%s", hex.Dump(wire), hex.Dump(wantWire))
	}
}
