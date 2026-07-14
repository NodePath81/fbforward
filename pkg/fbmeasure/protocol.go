package fbmeasure

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	frameSize    = 32
	frameVersion = 1
	frameKind    = 1
)

var frameMagic = [4]byte{'F', 'B', 'M', '1'}

type Protocol string

const (
	ProtocolTCP Protocol = "tcp"
	ProtocolUDP Protocol = "udp"
)

type frame [frameSize]byte

func newProbeFrame(sequence uint64) (frame, error) {
	var result frame
	copy(result[0:4], frameMagic[:])
	result[4] = frameVersion
	result[5] = frameKind
	binary.BigEndian.PutUint64(result[8:16], sequence)
	if _, err := rand.Read(result[16:]); err != nil {
		return frame{}, err
	}
	return result, nil
}

func parseFrame(data []byte) (frame, error) {
	if len(data) != frameSize {
		return frame{}, fmt.Errorf("invalid frame size: %d", len(data))
	}
	var result frame
	copy(result[:], data)
	if string(result[0:4]) != string(frameMagic[:]) {
		return frame{}, errors.New("invalid frame magic")
	}
	if result[4] != frameVersion {
		return frame{}, fmt.Errorf("unsupported frame version: %d", result[4])
	}
	if result[5] != frameKind {
		return frame{}, fmt.Errorf("unsupported frame kind: %d", result[5])
	}
	if result[6] != 0 || result[7] != 0 {
		return frame{}, errors.New("reserved frame fields must be zero")
	}
	return result, nil
}

func (f frame) sequence() uint64 {
	return binary.BigEndian.Uint64(f[8:16])
}
