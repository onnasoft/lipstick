package helper

import (
	"encoding/binary"
	"errors"
	"io"
)

// MaxDatagram is the largest UDP payload a frame can carry.
const MaxDatagram = 65507

// WriteFrame writes one datagram as a 2-byte big-endian length and the payload.
// A stream has no message boundaries, so the length keeps datagrams apart. The
// whole frame is written with one call, so frames from one writer never mix.
func WriteFrame(w io.Writer, payload []byte) error {
	if len(payload) > MaxDatagram {
		return errors.New("datagram too large for a frame")
	}
	buf := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(buf, uint16(len(payload)))
	copy(buf[2:], payload)
	_, err := w.Write(buf)
	return err
}

// ReadFrame reads one frame written by WriteFrame.
func ReadFrame(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(hdr[:]))
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}
