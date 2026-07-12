package decode_test

import (
	"testing"

	"github.com/mattcburns/shoal/internal/core/ai/decode"
)

func TestStripCodeFences(t *testing.T) {
	in := "```json\n{\"a\":1}\n```"
	got := decode.StripCodeFences(in)
	if got != `{"a":1}` {
		t.Fatalf("got %q", got)
	}
}

func TestExtractJSONObject(t *testing.T) {
	in := "Here you go:\n```json\n{\"serial\":\"ABC\"}\n```\nthanks"
	obj, err := decode.ExtractJSONObject(in)
	if err != nil {
		t.Fatal(err)
	}
	if obj != `{"serial":"ABC"}` {
		t.Fatalf("got %q", obj)
	}
}

func TestDecodeJSON(t *testing.T) {
	type sample struct {
		Serial string `json:"serial"`
	}
	got, err := decode.DecodeJSON[sample](`noise {"serial":"X1"} trailing`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Serial != "X1" {
		t.Fatalf("serial %q", got.Serial)
	}
}
