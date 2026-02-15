package sn76489

import "math"

// ToneZero controls how a tone register value of 0 is handled.
type ToneZero int

const (
	ToneZeroAsOne  ToneZero = iota // Sega: tone reg 0 behaves as 1
	ToneZeroAs1024                 // TI: tone reg 0 behaves as 1024
)

// Config describes the chip variant differences between TI and Sega versions.
type Config struct {
	LFSRBits       int    // 15 for TI, 16 for Sega
	WhiteNoiseTaps uint16 // Bitmask: 0x0003 for TI (bits 0,1), 0x0009 for Sega (bits 0,3)
	ToneZero       ToneZero
}

// Sega is the config for the Sega variant (SMS/GG/Genesis).
var Sega = Config{LFSRBits: 16, WhiteNoiseTaps: 0x0009, ToneZero: ToneZeroAsOne}

// TI is the config for the original TI SN76489.
var TI = Config{LFSRBits: 15, WhiteNoiseTaps: 0x0003, ToneZero: ToneZeroAs1024}

// Volume table: converts 4-bit volume to linear amplitude
// 0 = maximum volume, 15 = silence
// Each step is approximately -2dB
var volumeTable [16]float32

func init() {
	for i := 0; i < 15; i++ {
		volumeTable[i] = float32(math.Pow(10, -2.0*float64(i)/20.0))
	}
	volumeTable[15] = 0.0
}

// Emulates the SN76489 Programmable Sound Generator
// - 3 square wave tone channels
// - 1 noise channel
// - 4-bit volume per channel (0 = max, 15 = silent)
type SN76489 struct {
	// Tone channel registers (10-bit frequency dividers)
	toneReg [3]uint16
	// Tone channel counters
	toneCounter [3]uint16
	// Tone channel output state (high/low)
	toneOutput [3]bool

	// Noise channel
	noiseReg     uint8  // 3-bit: NF1 NF0 FB (shift rate and feedback mode)
	noiseCounter uint16 // Counter for noise
	noiseShift   uint16 // LFSR
	noiseToggle  bool   // Internal toggle (flips every counter period, like tone)
	noiseOut     bool   // Audio output (captured from LFSR on rising edge of toggle)

	// Volume registers (4-bit, 0=max, 15=off)
	volume [4]uint8 // 0-2 = tone channels, 3 = noise

	// Latch state for two-byte writes
	latchedChannel uint8 // Which channel is latched (0-3)
	latchedType    uint8 // 0 = tone/noise, 1 = volume

	// Variant-derived config
	feedbackShift  uint   // LFSRBits - 1 (14 for TI, 15 for Sega)
	lfsrInitial    uint16 // 1 << feedbackShift (0x4000 or 0x8000)
	whiteNoiseTaps uint16 // Copy from config
	toneZeroValue  uint16 // 1 for Sega, 1024 for TI

	// Clock info
	clocksPerSample float64
	clockCounter    float64
	clockDivider    int // Divides input clock by 16

	// Gain applied to mixed output (default 0.25 = /4.0)
	gain float32

	// Output buffers (used by GenerateSamples/Run)
	channelBuffers [4][]float32 // per-channel raw amplitude buffers
	mixBuffer      []float32    // mono mix output (filled by GetBuffer)
	bufferPos      int
}

// New creates a new SN76489 instance
// clockFreq is the chip clock frequency (typically 3579545 Hz for SMS)
// sampleRate is the audio output sample rate (e.g., 44100 Hz)
// bufferSize is the number of samples per buffer
// config selects the chip variant (Sega or TI)
func New(clockFreq int, sampleRate int, bufferSize int, config Config) *SN76489 {
	feedbackShift := uint(config.LFSRBits - 1)
	lfsrInitial := uint16(1) << feedbackShift
	toneZeroValue := uint16(1)
	if config.ToneZero == ToneZeroAs1024 {
		toneZeroValue = 1024
	}

	p := &SN76489{
		clocksPerSample: float64(clockFreq) / float64(sampleRate),
		gain:            0.25,
		mixBuffer:       make([]float32, bufferSize),
		channelBuffers: [4][]float32{
			make([]float32, bufferSize),
			make([]float32, bufferSize),
			make([]float32, bufferSize),
			make([]float32, bufferSize),
		},
		noiseShift:     lfsrInitial,
		feedbackShift:  feedbackShift,
		lfsrInitial:    lfsrInitial,
		whiteNoiseTaps: config.WhiteNoiseTaps,
		toneZeroValue:  toneZeroValue,
	}
	// Initialize volumes to silent
	for i := range p.volume {
		p.volume[i] = 0x0F
	}
	return p
}

// Reset resets all chip state to power-on defaults.
// Gain is not reset since it is host-side audio config, not chip state.
func (s *SN76489) Reset() {
	s.toneReg = [3]uint16{}
	s.toneCounter = [3]uint16{}
	s.toneOutput = [3]bool{}
	s.noiseReg = 0
	s.noiseCounter = 0
	s.noiseShift = s.lfsrInitial
	s.noiseToggle = false
	s.noiseOut = false
	for i := range s.volume {
		s.volume[i] = 0x0F
	}
	s.latchedChannel = 0
	s.latchedType = 0
	s.clockDivider = 0
	s.clockCounter = 0
	s.bufferPos = 0
}

// Write handles writes to the SN76489
func (s *SN76489) Write(value uint8) {
	if value&0x80 != 0 {
		// LATCH/DATA byte: 1 CC T DDDD
		// CC = channel (0-2 tone, 3 noise)
		// T = type (0 = tone/noise, 1 = volume)
		// DDDD = data
		s.latchedChannel = (value >> 5) & 0x03
		s.latchedType = (value >> 4) & 0x01
		data := value & 0x0F

		if s.latchedType == 1 {
			// Volume write
			s.volume[s.latchedChannel] = data
		} else {
			// Tone/noise write
			if s.latchedChannel < 3 {
				// Tone channel: update low 4 bits
				s.toneReg[s.latchedChannel] = (s.toneReg[s.latchedChannel] & 0x3F0) | uint16(data)
			} else {
				// Noise channel control
				s.noiseReg = data & 0x07
				s.noiseShift = s.lfsrInitial
			}
		}
	} else {
		// DATA byte: 0 X DDDDDD
		if s.latchedType == 0 {
			if s.latchedChannel < 3 {
				// Tone channel: update high 6 bits
				data := uint16(value & 0x3F)
				s.toneReg[s.latchedChannel] = (s.toneReg[s.latchedChannel] & 0x0F) | (data << 4)
			} else {
				// Noise: update from low 3 bits of data byte, reset LFSR
				s.noiseReg = value & 0x07
				s.noiseShift = s.lfsrInitial
			}
		}
	}
}

// Clock advances the SN76489 by one clock cycle (internal, doesn't generate samples)
func (s *SN76489) Clock() {
	// SN76489 divides input clock by 16
	s.clockDivider++
	if s.clockDivider < 16 {
		return
	}
	s.clockDivider = 0

	// Update tone channels
	for i := 0; i < 3; i++ {
		if s.toneCounter[i] > 0 {
			s.toneCounter[i]--
		} else {
			// Reload counter and flip output
			if s.toneReg[i] == 0 {
				s.toneCounter[i] = s.toneZeroValue
			} else {
				s.toneCounter[i] = s.toneReg[i]
			}
			s.toneOutput[i] = !s.toneOutput[i]
		}
	}

	// Update noise channel
	if s.noiseCounter > 0 {
		s.noiseCounter--
	} else {
		// Reload counter
		rate := s.noiseReg & 0x03
		switch rate {
		case 0:
			s.noiseCounter = 0x10
		case 1:
			s.noiseCounter = 0x20
		case 2:
			s.noiseCounter = 0x40
		case 3:
			// Use tone channel 2's frequency
			if s.toneReg[2] == 0 {
				s.noiseCounter = s.toneZeroValue
			} else {
				s.noiseCounter = s.toneReg[2]
			}
		}

		// Toggle noise state (like tone channels)
		s.noiseToggle = !s.noiseToggle

		// Only shift LFSR and update output on the rising edge,
		// matching real hardware where the LFSR clocks at half
		// the counter rate.
		if s.noiseToggle {
			s.noiseOut = (s.noiseShift & 1) != 0

			// Calculate feedback bit
			var feedback uint16
			if s.noiseReg&0x04 != 0 {
				// White noise: parity of tapped bits
				tapped := s.noiseShift & s.whiteNoiseTaps
				tapped ^= tapped >> 8
				tapped ^= tapped >> 4
				tapped ^= tapped >> 2
				tapped ^= tapped >> 1
				feedback = (tapped & 1) << s.feedbackShift
			} else {
				// Periodic noise: feedback the output bit only (repeating pattern)
				feedback = (s.noiseShift & 1) << s.feedbackShift
			}

			s.noiseShift = (s.noiseShift >> 1) | feedback
		}
	}
}

// Sample generates one audio sample using unipolar output matching
// real hardware behavior: channels contribute their volume level
// when output is high, and 0 when low.
func (s *SN76489) Sample() float32 {
	var sample float32 = 0

	// Mix tone channels (unipolar: high = +vol, low = 0)
	for i := 0; i < 3; i++ {
		if s.toneOutput[i] {
			sample += volumeTable[s.volume[i]]
		}
	}

	// Mix noise channel (uses noiseOut captured at LFSR shift time)
	if s.noiseOut {
		sample += volumeTable[s.volume[3]]
	}

	return sample * s.gain
}

// ResetBuffer resets the internal buffer position to 0.
// Called once at the start of each frame when using Run for cycle-accurate emulation.
func (s *SN76489) ResetBuffer() {
	s.bufferPos = 0
}

// Run advances the chip by the given number of clocks, accumulating samples
// into the per-channel buffers from the current position. Unlike GenerateSamples,
// it does not reset the buffer position, allowing multiple Run calls (with
// register writes in between) within a single frame for cycle-accurate emulation.
// Returns the number of samples dropped due to buffer overflow.
func (s *SN76489) Run(clocks int) int {
	dropped := 0
	for i := 0; i < clocks; i++ {
		s.Clock()
		s.clockCounter++
		if s.clockCounter >= s.clocksPerSample {
			s.clockCounter -= s.clocksPerSample
			if s.bufferPos < len(s.mixBuffer) {
				for ch := 0; ch < 3; ch++ {
					if s.toneOutput[ch] {
						s.channelBuffers[ch][s.bufferPos] = volumeTable[s.volume[ch]]
					} else {
						s.channelBuffers[ch][s.bufferPos] = 0
					}
				}
				if s.noiseOut {
					s.channelBuffers[3][s.bufferPos] = volumeTable[s.volume[3]]
				} else {
					s.channelBuffers[3][s.bufferPos] = 0
				}
				s.bufferPos++
			} else {
				dropped++
			}
		}
	}
	return dropped
}

// GenerateSamples fills the buffer with audio samples.
// Called once per frame with the number of SN76489 clocks that occurred.
// Returns the number of samples dropped due to buffer overflow.
func (s *SN76489) GenerateSamples(clocks int) int {
	s.ResetBuffer()
	return s.Run(clocks)
}

// GetBuffer mixes the 4 per-channel buffers into a mono buffer with gain
// applied and returns it along with the number of valid samples.
// The returned slice is reused across calls; copy it if you need to retain
// the data beyond the next GetBuffer or GenerateSamples call.
func (s *SN76489) GetBuffer() ([]float32, int) {
	for i := 0; i < s.bufferPos; i++ {
		s.mixBuffer[i] = (s.channelBuffers[0][i] + s.channelBuffers[1][i] +
			s.channelBuffers[2][i] + s.channelBuffers[3][i]) * s.gain
	}
	return s.mixBuffer, s.bufferPos
}

// GetChannelBuffers returns the 4 raw per-channel amplitude buffers and the
// number of valid samples. No gain is applied. Useful for stereo panning
// (Game Gear) or debug visualization.
// The returned slices are reused across calls; copy them if you need to retain
// the data beyond the next Run or GenerateSamples call.
func (s *SN76489) GetChannelBuffers() ([4][]float32, int) {
	return s.channelBuffers, s.bufferPos
}

// SetGain sets the gain applied to mixed output by GetBuffer and Sample.
// Default is 0.25 (equivalent to dividing by 4). Genesis emulators can use
// this to control PSG level relative to YM2612.
func (s *SN76489) SetGain(gain float32) {
	s.gain = gain
}

// GetGain returns the current gain value.
func (s *SN76489) GetGain() float32 {
	return s.gain
}

// ClocksPerSample returns the number of input clocks per output sample
// (clockFreq / sampleRate). Useful for pre-calculating buffer sizes:
// samplesPerFrame = totalClocks / ClocksPerSample().
func (s *SN76489) ClocksPerSample() float64 {
	return s.clocksPerSample
}

// GetToneReg returns the 10-bit tone register for the given channel (0-2)
func (s *SN76489) GetToneReg(ch int) uint16 {
	return s.toneReg[ch]
}

// GetVolume returns the 4-bit volume for the given channel (0-3)
func (s *SN76489) GetVolume(ch int) uint8 {
	return s.volume[ch]
}

// GetNoiseReg returns the noise control register
func (s *SN76489) GetNoiseReg() uint8 {
	return s.noiseReg
}

// GetVolumeTable returns the volume lookup table (for testing)
func GetVolumeTable() []float32 {
	return volumeTable[:]
}

// GetNoiseShift returns the current LFSR state (for testing)
func (s *SN76489) GetNoiseShift() uint16 {
	return s.noiseShift
}
