package helper

import (
	"bytes"
	"testing"
)

// Datagrams keep their boundaries, including empty ones, through a plain stream.
func TestFramesKeepDatagramBoundaries(t *testing.T) {
	var stream bytes.Buffer
	in := [][]byte{[]byte("hello"), {}, bytes.Repeat([]byte{7}, MaxDatagram)}
	for _, d := range in {
		if err := WriteFrame(&stream, d); err != nil {
			t.Fatal(err)
		}
	}
	for i, want := range in {
		got, err := ReadFrame(&stream)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("frame %d: got %d bytes, %v; want %d bytes", i, len(got), err, len(want))
		}
	}
}

func TestWriteFrameRefusesOversizedDatagram(t *testing.T) {
	if err := WriteFrame(&bytes.Buffer{}, make([]byte, MaxDatagram+1)); err == nil {
		t.Fatal("oversized datagram accepted")
	}
}
