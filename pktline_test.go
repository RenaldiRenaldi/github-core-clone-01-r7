package pktline

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
