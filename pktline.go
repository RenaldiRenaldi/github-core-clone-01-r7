package pktline

import (
	"fmt"
	"io"
)

// PacketKind identifies the three control packets used by Git's pkt-line
// transport in addition to ordinary data packets.
type PacketKind uint8

const (
	PacketData PacketKind = iota
	PacketFlush
	PacketDelimiter
	PacketResponseEnd
)

// EncodePktLine encodes payload as a Git smart-HTTP pkt-line. An empty
// payload is encoded as the special flush-pkt.
func EncodePktLine(payload []byte) []byte {
	if len(payload) == 0 {
		return []byte("0000")
	}
	length := len(payload) + 4
	if length > 0xffff {
		return nil
	}
	encoded := make([]byte, 4+len(payload))
	copy(encoded[:4], fmt.Sprintf("%04x", length))
	copy(encoded[4:], payload)
	return encoded
}

// EncodeControlPacket serializes a control packet. It keeps control markers
// distinct from data packets, which is important when assembling multi-part
// smart-HTTP exchanges.
func EncodeControlPacket(kind PacketKind) []byte {
	switch kind {
	case PacketFlush:
		return []byte("0000")
	case PacketDelimiter:
		return []byte("0001")
	case PacketResponseEnd:
		return []byte("0002")
	default:
		return nil
	}
}

// DecodePktLine reads one Git smart-HTTP pkt-line. A flush-pkt returns
// (nil, nil).
func DecodePktLine(r io.Reader) ([]byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	var length int
	for _, digit := range header {
		length <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			length += int(digit - '0')
		case digit >= 'a' && digit <= 'f':
			length += int(digit-'a') + 10
		case digit >= 'A' && digit <= 'F':
			length += int(digit-'A') + 10
		default:
			return nil, fmt.Errorf("invalid pkt-line length digit %q", digit)
		}
	}
	if length == 0 || length == 1 || length == 2 {
		return nil, nil
	}
	if length < 4 {
		return nil, fmt.Errorf("invalid pkt-line length %d: less than header size", length)
	}
	payload := make([]byte, length-4)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}
