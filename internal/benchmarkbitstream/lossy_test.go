package benchmarkbitstream

import (
	"encoding/binary"
	"testing"
)

func TestParseLossyReportsVP8Partitions(t *testing.T) {
	payload := make([]byte, 10+7+13)
	firstPartitionTag := uint32(7 << 5)
	payload[0] = byte(firstPartitionTag)
	payload[1] = byte(firstPartitionTag >> 8)
	payload[2] = byte(firstPartitionTag >> 16)
	data := make([]byte, 20+len(payload))
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:12], "WEBP")
	copy(data[12:16], "VP8 ")
	binary.LittleEndian.PutUint32(data[16:20], uint32(len(payload)))
	copy(data[20:], payload)

	layout, err := ParseLossy(data)
	if err != nil {
		t.Fatal(err)
	}
	if layout.FileBytes != len(data) || layout.ContainerAndOtherBytes != 20 || layout.VP8PayloadBytes != len(payload) || layout.FrameHeaderBytes != 10 || layout.FirstPartitionBytes != 7 || layout.ResidualPartitionBytes != 13 {
		t.Fatalf("layout = %#v", layout)
	}
}

func TestParseLossyRejectsInvalidData(t *testing.T) {
	for _, data := range [][]byte{
		nil,
		[]byte("RIFF\x04\x00\x00\x00WEBP"),
		[]byte("RIFF\x10\x00\x00\x00WEBPVP8 \xff\xff\xff\xff"),
	} {
		if _, err := ParseLossy(data); err == nil {
			t.Fatalf("ParseLossy accepted %q", data)
		}
	}
}
