package milesight

import (
	"testing"
	"time"
)

// TestDecode_RoutesUCSeriesToTheirOwnTokenizer proves the shared Decode()
// dispatcher special-cases UC100/UC300/UC501 (variable-length uc.go
// tokenizer) before falling through to the generic ParseMilesightTLV +
// modelDecoders path used by every fixed-length device (including UC511 and
// VS370, which look like UC-series by name but have no RS485 port at all).
func TestDecode_RoutesUCSeriesToTheirOwnTokenizer(t *testing.T) {
	battery := []byte{0x01, 0x75, 0x5A} // valid "battery 90%" frame for uc501/uc511/vs370

	cases := []struct {
		model string
		want  int // expected record count from this exact payload
	}{
		{"UC100", 0}, // no (channel,type) in this payload matches UC100's Modbus/read-error markers
		{"UC300", 0}, // same — UC300 also finds nothing to decode from a bare battery frame
		{"UC501", 1}, // UC501 has a fixed battery channel (0x01,0x75)
		{"UC511", 1}, // UC511 also has a fixed battery channel
		{"VS370", 1}, // VS370 also has a fixed battery channel
		{"AT101", 1}, // sanity: an already-existing fixed-TLV device behaves the same way
	}
	for _, c := range cases {
		t.Run(c.model, func(t *testing.T) {
			records := Decode(c.model, "", battery, "dev-1", "chirpstackv4", time.Now())
			if len(records) != c.want {
				t.Errorf("model=%s: got %d records, want %d: %+v", c.model, len(records), c.want, records)
			}
		})
	}
}

func TestDecode_UnknownModelReturnsNil(t *testing.T) {
	if got := Decode("NOT_A_REAL_MODEL", "", []byte{0x01, 0x75, 0x5A}, "dev-1", "chirpstackv4", time.Now()); got != nil {
		t.Errorf("expected nil for an unregistered model, got %+v", got)
	}
}
