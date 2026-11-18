package main

import (
	"bytes"
	"testing"
)

func TestPktLineRoundTrip(t *testing.T) {
	want := []byte("hello")
	encoded := EncodePktLine(want)
	if got := string(encoded[:4]); got != "0009" {
		t.Fatalf("length header = %q, want %q", got, "0009")
	}
	decoded, err := DecodePktLine(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("DecodePktLine() error = %v", err)
	}
	if !bytes.Equal(decoded, want) {
		t.Fatalf("DecodePktLine() = %q, want %q", decoded, want)
	}
}

func TestPktLineFlush(t *testing.T) {
	encoded := EncodePktLine(nil)
	if string(encoded) != "0000" {
		t.Fatalf("EncodePktLine(nil) = %q, want %q", encoded, "0000")
	}
	decoded, err := DecodePktLine(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("DecodePktLine() error = %v", err)
	}
	if decoded != nil {
		t.Fatalf("DecodePktLine() = %v, want nil", decoded)
	}
}

func TestControlPacketDecoderKeepsStreamPosition(t *testing.T) {
	stream := bytes.NewBufferString("00010005x")
	got, err := DecodePktLine(stream)
	if err != nil || got != nil {
		t.Fatalf("delimiter decode = %q, %v", got, err)
	}
	got, err = DecodePktLine(stream)
	if err != nil || string(got) != "x" {
		t.Fatalf("next packet = %q, %v", got, err)
	}
}

func TestPktLineLargePayload(t *testing.T) {
	want := bytes.Repeat([]byte("x"), 1000)
	encoded := EncodePktLine(want)
	if got := string(encoded[:4]); got != "03ec" {
		t.Fatalf("length header = %q, want %q", got, "03ec")
	}
	decoded, err := DecodePktLine(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("DecodePktLine() error = %v", err)
	}
	if !bytes.Equal(decoded, want) {
		t.Fatalf("decoded payload length = %d, want %d", len(decoded), len(want))
	}
}

func TestControlPacketsAreExactAndDoNotConsumeFollowingPacket(t *testing.T) {
	for _, tc := range []struct {
		kind PacketKind
		wire string
	}{
		{PacketFlush, "0000"},
		{PacketDelimiter, "0001"},
		{PacketResponseEnd, "0002"},
	} {
		if got := string(EncodeControlPacket(tc.kind)); got != tc.wire {
			t.Fatalf("control packet = %q, want %q", got, tc.wire)
		}
		stream := bytes.NewBufferString(tc.wire + "0005x")
		got, err := DecodePktLine(stream)
		if err != nil || got != nil {
			t.Fatalf("DecodePktLine(%q) = %q, %v", tc.wire, got, err)
		}
		next, err := DecodePktLine(stream)
		if err != nil || string(next) != "x" {
			t.Fatalf("following data packet = %q, %v", next, err)
		}
	}
}

func TestMalformedAndShortPktLines(t *testing.T) {
	for _, wire := range []string{"0003", "00xz", "000"} {
		if _, err := DecodePktLine(bytes.NewBufferString(wire)); err == nil {
			t.Errorf("DecodePktLine(%q) unexpectedly succeeded", wire)
		}
	}
}

func TestPktLineRejectsOversizePayload(t *testing.T) {
	if got := EncodePktLine(bytes.Repeat([]byte{'x'}, 0xffff-3)); got != nil {
		t.Fatalf("oversize packet length = %d, want nil", len(got))
	}
}
