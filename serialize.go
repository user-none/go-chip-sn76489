package sn76489

import (
	"encoding/binary"
	"errors"
	"math"
)

const serializeVersion = 1
const sn76489SerializeSize = 40

// SerializeSize returns the number of bytes needed to serialize the chip state.
// The value is constant and can be used to pre-allocate a reusable buffer.
func (s *SN76489) SerializeSize() int {
	return sn76489SerializeSize
}

// Serialize writes all mutable chip state into buf in a compact little-endian
// binary format. Returns an error if len(buf) < SerializeSize(). Variant-derived
// constants and audio config are not included — the caller handles those via
// the New constructor and SetGain.
func (s *SN76489) Serialize(buf []byte) error {
	if len(buf) < sn76489SerializeSize {
		return errors.New("sn76489: serialize buffer too small")
	}

	buf[0] = serializeVersion
	for i := 0; i < 3; i++ {
		binary.LittleEndian.PutUint16(buf[1+i*2:], s.toneReg[i])
	}
	for i := 0; i < 3; i++ {
		binary.LittleEndian.PutUint16(buf[7+i*2:], s.toneCounter[i])
	}
	for i := 0; i < 3; i++ {
		buf[13+i] = boolByte(s.toneOutput[i])
	}
	buf[16] = s.noiseReg
	binary.LittleEndian.PutUint16(buf[17:], s.noiseCounter)
	binary.LittleEndian.PutUint16(buf[19:], s.noiseShift)
	buf[21] = boolByte(s.noiseOutput)
	for i := 0; i < 4; i++ {
		buf[22+i] = s.volume[i]
	}
	buf[26] = s.latchedChannel
	buf[27] = s.latchedType
	binary.LittleEndian.PutUint32(buf[28:], uint32(int32(s.clockDivider)))
	binary.LittleEndian.PutUint64(buf[32:], math.Float64bits(s.clockCounter))
	return nil
}

// Deserialize restores all mutable chip state from buf, which must have been
// produced by Serialize. Returns an error if the buffer is too small or was
// produced by an incompatible version. Variant-derived constants and audio
// config are not modified — the caller handles those via the New constructor
// and SetGain.
func (s *SN76489) Deserialize(buf []byte) error {
	if len(buf) < sn76489SerializeSize {
		return errors.New("sn76489: deserialize buffer too small")
	}
	if buf[0] != serializeVersion {
		return errors.New("sn76489: unsupported serialize version")
	}

	for i := 0; i < 3; i++ {
		s.toneReg[i] = binary.LittleEndian.Uint16(buf[1+i*2:])
	}
	for i := 0; i < 3; i++ {
		s.toneCounter[i] = binary.LittleEndian.Uint16(buf[7+i*2:])
	}
	for i := 0; i < 3; i++ {
		s.toneOutput[i] = buf[13+i] != 0
	}
	s.noiseReg = buf[16]
	s.noiseCounter = binary.LittleEndian.Uint16(buf[17:])
	s.noiseShift = binary.LittleEndian.Uint16(buf[19:])
	s.noiseOutput = buf[21] != 0
	for i := 0; i < 4; i++ {
		s.volume[i] = buf[22+i]
	}
	s.latchedChannel = buf[26]
	s.latchedType = buf[27]
	s.clockDivider = int(int32(binary.LittleEndian.Uint32(buf[28:])))
	s.clockCounter = math.Float64frombits(binary.LittleEndian.Uint64(buf[32:]))
	s.bufferPos = 0
	return nil
}

func boolByte(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}
