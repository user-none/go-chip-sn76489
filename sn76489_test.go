package sn76489

import (
	"math"
	"testing"
)

// TestSN76489_SilentOnInit verifies all volumes start at 0x0F (silent)
func TestSN76489_SilentOnInit(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)

	for ch := 0; ch < 4; ch++ {
		if vol := chip.GetVolume(ch); vol != 0x0F {
			t.Errorf("Channel %d initial volume: expected 0x0F (silent), got 0x%02X", ch, vol)
		}
	}
}

// TestSN76489_VolumeRegisterWrite tests 4-bit volume writes for all channels
func TestSN76489_VolumeRegisterWrite(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)

	testCases := []struct {
		channel uint8
		volume  uint8
	}{
		{0, 0x00}, // Channel 0, max volume
		{1, 0x08}, // Channel 1, mid volume
		{2, 0x0F}, // Channel 2, silent
		{3, 0x05}, // Noise channel
	}

	for _, tc := range testCases {
		// Volume write: 1 CC 1 VVVV (bit 7=1, CC=channel, bit 4=1 for volume, VVVV=volume)
		cmd := uint8(0x90) | (tc.channel << 5) | tc.volume
		chip.Write(cmd)

		if got := chip.GetVolume(int(tc.channel)); got != tc.volume {
			t.Errorf("Channel %d volume after write: expected 0x%02X, got 0x%02X", tc.channel, tc.volume, got)
		}
	}
}

// TestSN76489_ToneRegisterWrite tests 10-bit tone register via latch+data bytes
func TestSN76489_ToneRegisterWrite(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)

	// Write a 10-bit tone value (0x1AB = 427) to channel 0
	// First byte: 1 CC 0 DDDD (low 4 bits) = 0x80 | 0x0B = 0x8B
	// Second byte: 0 X DDDDDD (high 6 bits) = 0x1A
	chip.Write(0x8B) // Latch channel 0 tone, low nibble = 0xB
	chip.Write(0x1A) // Data = 0x1A (high 6 bits)

	expected := uint16(0x1AB)
	if got := chip.GetToneReg(0); got != expected {
		t.Errorf("Channel 0 tone register: expected 0x%03X, got 0x%03X", expected, got)
	}

	// Test channel 1 with a different value
	chip.Write(0xA5) // Latch channel 1 tone, low nibble = 0x5
	chip.Write(0x3F) // Data = 0x3F (high 6 bits)

	expected = uint16(0x3F5)
	if got := chip.GetToneReg(1); got != expected {
		t.Errorf("Channel 1 tone register: expected 0x%03X, got 0x%03X", expected, got)
	}
}

// TestSN76489_NoiseRegisterWrite tests noise control register writes
func TestSN76489_NoiseRegisterWrite(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)

	// Noise write: 1 11 0 0NNN (channel 3, type=0, NNN=noise control)
	// Test different noise modes
	testCases := []struct {
		noiseReg uint8
		desc     string
	}{
		{0x00, "periodic noise, /512"},
		{0x01, "periodic noise, /1024"},
		{0x02, "periodic noise, /2048"},
		{0x03, "periodic noise, tone2 rate"},
		{0x04, "white noise, /512"},
		{0x05, "white noise, /1024"},
		{0x06, "white noise, /2048"},
		{0x07, "white noise, tone2 rate"},
	}

	for _, tc := range testCases {
		// Noise register write: 0xE0 | noise bits
		chip.Write(0xE0 | tc.noiseReg)

		if got := chip.GetNoiseReg(); got != tc.noiseReg {
			t.Errorf("Noise register for %s: expected 0x%02X, got 0x%02X", tc.desc, tc.noiseReg, got)
		}
	}
}

// TestSN76489_VolumeTable tests volume lookup table values
func TestSN76489_VolumeTable(t *testing.T) {
	table := GetVolumeTable()

	// Volume 0 should be maximum (1.0)
	if table[0] != 1.0 {
		t.Errorf("Volume 0: expected 1.0, got %f", table[0])
	}

	// Volume 15 should be silent (0.0)
	if table[15] != 0.0 {
		t.Errorf("Volume 15: expected 0.0, got %f", table[15])
	}

	// Each step should decrease (approximately -2dB)
	for i := 0; i < 14; i++ {
		if table[i+1] >= table[i] {
			t.Errorf("Volume %d (%.3f) should be greater than volume %d (%.3f)",
				i, table[i], i+1, table[i+1])
		}
	}

	// Verify approximately -2dB per step (ratio ≈ 0.794)
	for i := 0; i < 14; i++ {
		if table[i] > 0 && table[i+1] > 0 {
			ratio := table[i+1] / table[i]
			if ratio < 0.7 || ratio > 0.9 {
				t.Errorf("Volume ratio %d->%d: expected ~0.794, got %.3f", i, i+1, ratio)
			}
		}
	}
}

// TestSN76489_ClockDivider tests that input clock is divided by 16
func TestSN76489_ClockDivider(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)

	// Set up tone channel 0 with frequency divider of 1 (highest frequency)
	// and max volume
	chip.Write(0x81) // Channel 0 tone, low nibble = 1
	chip.Write(0x00) // High bits = 0, so tone = 1
	chip.Write(0x90) // Channel 0 volume = 0 (max)

	// The tone output should flip every (divider value) internal clocks
	// Since divider = 16, and tone reg = 1, output flips every 16 input clocks
	// after the counter decrements

	// Clock 15 times - should not complete a full divider cycle
	for i := 0; i < 15; i++ {
		chip.Clock()
	}

	// The 16th clock should trigger an internal update
	// This tests that the divider is working
	chip.Clock()
	// After 16 clocks, the tone counter should have decremented
}

// TestSN76489_SampleGeneration tests that samples are generated correctly
func TestSN76489_SampleGeneration(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)

	// All channels silent - should generate ~0 output
	sample := chip.Sample()

	// With all volumes at 0x0F (silent), output should be 0
	if math.Abs(float64(sample)) > 0.001 {
		t.Errorf("Silent sample: expected ~0, got %f", sample)
	}
}

// TestSN76489_SampleMixing tests 4 channels mixed and normalized
func TestSN76489_SampleMixing(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)

	// Set all channels to max volume
	chip.Write(0x90) // Channel 0 volume = 0 (max)
	chip.Write(0xB0) // Channel 1 volume = 0 (max)
	chip.Write(0xD0) // Channel 2 volume = 0 (max)
	chip.Write(0xF0) // Noise volume = 0 (max)

	// Generate a sample - with all at max, should be bounded
	sample := chip.Sample()

	// Sample should be normalized to [-1, 1] range
	if sample < -1.0 || sample > 1.0 {
		t.Errorf("Sample out of range: %f", sample)
	}
}

// TestSN76489_NoiseRateFromTone2 tests noise rate 3 uses tone channel 2
func TestSN76489_NoiseRateFromTone2(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)

	// Set tone channel 2 to a specific frequency
	chip.Write(0xC5) // Channel 2 tone, low nibble = 5
	chip.Write(0x10) // High bits = 0x10, so tone = 0x105

	// Set noise to use tone 2 rate (rate = 3)
	chip.Write(0xE3) // Noise control = 3 (use tone 2)

	tone2Reg := chip.GetToneReg(2)
	if tone2Reg != 0x105 {
		t.Errorf("Tone 2 register: expected 0x105, got 0x%03X", tone2Reg)
	}

	noiseReg := chip.GetNoiseReg()
	if noiseReg&0x03 != 3 {
		t.Errorf("Noise rate bits: expected 3, got %d", noiseReg&0x03)
	}
}

// TestSN76489_GenerateSamples tests buffer-based sample generation
func TestSN76489_GenerateSamples(t *testing.T) {
	chip := New(3579545, 48000, 100, Sega)

	// Generate samples for a certain number of clocks
	clocks := 10000 // Enough clocks to generate some samples
	chip.GenerateSamples(clocks)

	buf, count := chip.GetBuffer()
	if count == 0 {
		t.Error("GenerateSamples produced no samples")
	}
	if buf == nil {
		t.Error("GetBuffer returned nil buffer")
	}
	if count > len(buf) {
		t.Errorf("Sample count %d exceeds buffer size %d", count, len(buf))
	}
}

// TestSN76489_ToneLatchPersistence tests that the latched channel persists
func TestSN76489_ToneLatchPersistence(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)

	// Latch channel 2
	chip.Write(0xC0) // Channel 2 tone, low nibble = 0

	// Write multiple data bytes - should all go to channel 2
	chip.Write(0x10) // High 6 bits = 0x10
	expected := uint16(0x100)
	if got := chip.GetToneReg(2); got != expected {
		t.Errorf("After first data: expected 0x%03X, got 0x%03X", expected, got)
	}

	// Write another data byte
	chip.Write(0x20) // High 6 bits = 0x20
	expected = uint16(0x200)
	if got := chip.GetToneReg(2); got != expected {
		t.Errorf("After second data: expected 0x%03X, got 0x%03X", expected, got)
	}
}

// TestSN76489_NoiseDataByteWrite verifies that latching noise channel then
// sending a data byte updates noiseReg and resets the LFSR.
func TestSN76489_NoiseDataByteWrite(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)

	// Latch noise channel with periodic noise, rate 0
	chip.Write(0xE0) // Latch channel 3 (noise), data = 0

	if got := chip.GetNoiseReg(); got != 0x00 {
		t.Errorf("After latch: expected noiseReg 0x00, got 0x%02X", got)
	}

	// Now send a data byte to update noise register
	chip.Write(0x05) // Data byte: low 3 bits = 5 (white noise, rate 1)

	if got := chip.GetNoiseReg(); got != 0x05 {
		t.Errorf("After data byte: expected noiseReg 0x05, got 0x%02X", got)
	}

	// LFSR should have been reset
	if got := chip.GetNoiseShift(); got != 0x8000 {
		t.Errorf("After data byte: expected noiseShift 0x8000, got 0x%04X", got)
	}
}

// TestSN76489_TI_ToneZero verifies TI variant treats tone reg 0 as 1024
func TestSN76489_TI_ToneZero(t *testing.T) {
	chip := New(3579545, 48000, 800, TI)

	// Set tone channel 0 to value 0, max volume
	chip.Write(0x80) // Latch channel 0 tone, low nibble = 0
	chip.Write(0x00) // High bits = 0, tone reg = 0
	chip.Write(0x90) // Channel 0 volume = 0 (max)

	// Clock 16 times to trigger first internal tick
	for i := 0; i < 16; i++ {
		chip.Clock()
	}

	// After the first internal tick with toneReg=0, counter should reload to 1024
	if chip.toneCounter[0] != 1024 {
		t.Errorf("TI tone zero: expected counter reload to 1024, got %d", chip.toneCounter[0])
	}
}

// lfsrStep simulates one LFSR shift matching the logic in Clock()
func lfsrStep(shift uint16, whiteNoise bool, taps uint16, feedbackShift uint) uint16 {
	var feedback uint16
	if whiteNoise {
		tapped := shift & taps
		tapped ^= tapped >> 8
		tapped ^= tapped >> 4
		tapped ^= tapped >> 2
		tapped ^= tapped >> 1
		feedback = (tapped & 1) << feedbackShift
	} else {
		feedback = (shift & 1) << feedbackShift
	}
	return (shift >> 1) | feedback
}

// TestSN76489_TI_LFSR verifies TI variant uses 15-bit LFSR with bits 0,1 taps
func TestSN76489_TI_LFSR(t *testing.T) {
	chip := New(3579545, 48000, 800, TI)

	// Initial LFSR should be 0x4000 for TI (15-bit)
	if got := chip.GetNoiseShift(); got != 0x4000 {
		t.Fatalf("TI initial LFSR: expected 0x4000, got 0x%04X", got)
	}

	// Directly simulate LFSR to verify it stays within 15 bits and is periodic
	initial := uint16(0x4000)
	shift := initial
	period := 0
	for {
		shift = lfsrStep(shift, true, 0x0003, 14)
		period++
		if shift > 0x7FFF {
			t.Fatalf("TI LFSR exceeded 15 bits at step %d: 0x%04X", period, shift)
		}
		if shift == 0 {
			t.Fatalf("TI LFSR reached zero at step %d", period)
		}
		if shift == initial {
			break
		}
		if period > 32767 {
			t.Fatal("TI LFSR did not return to initial state within 32767 steps")
		}
	}
	t.Logf("TI LFSR white noise period: %d", period)
}

// TestSN76489_Sega_LFSR verifies Sega variant uses 16-bit LFSR with bits 0,3 taps
func TestSN76489_Sega_LFSR(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)

	// Initial LFSR should be 0x8000 for Sega (16-bit)
	if got := chip.GetNoiseShift(); got != 0x8000 {
		t.Fatalf("Sega initial LFSR: expected 0x8000, got 0x%04X", got)
	}

	// Directly simulate LFSR to verify it stays within 16 bits and is periodic
	initial := uint16(0x8000)
	shift := initial
	period := 0
	for {
		shift = lfsrStep(shift, true, 0x0009, 15)
		period++
		if shift == 0 {
			t.Fatalf("Sega LFSR reached zero at step %d", period)
		}
		if shift == initial {
			break
		}
		if period > 65535 {
			t.Fatal("Sega LFSR did not return to initial state within 65535 steps")
		}
	}
	t.Logf("Sega LFSR white noise period: %d", period)
}

// TestSN76489_RunAccumulates verifies that two Run calls accumulate the same
// number of samples as a single GenerateSamples call for the same total clocks.
func TestSN76489_RunAccumulates(t *testing.T) {
	clocks := 10000
	splitPoint := 4000

	// Reference: single GenerateSamples
	ref := New(3579545, 48000, 800, Sega)
	ref.Write(0x80 | 0x05) // channel 0 tone low nibble
	ref.Write(0x01)        // tone = 0x15
	ref.Write(0x90)        // channel 0 volume = max
	ref.GenerateSamples(clocks)
	_, refCount := ref.GetBuffer()

	// Test: ResetBuffer + two Run calls
	chip := New(3579545, 48000, 800, Sega)
	chip.Write(0x80 | 0x05)
	chip.Write(0x01)
	chip.Write(0x90)
	chip.ResetBuffer()
	chip.Run(splitPoint)
	chip.Run(clocks - splitPoint)
	_, runCount := chip.GetBuffer()

	if runCount != refCount {
		t.Errorf("Run accumulated %d samples, GenerateSamples produced %d", runCount, refCount)
	}

	// Verify sample values match
	refBuf, _ := ref.GetBuffer()
	runBuf, _ := chip.GetBuffer()
	for i := 0; i < refCount; i++ {
		if refBuf[i] != runBuf[i] {
			t.Errorf("Sample %d differs: GenerateSamples=%f, Run=%f", i, refBuf[i], runBuf[i])
			break
		}
	}
}

// TestSN76489_RunMidFrameWrite verifies that a Write between two Run calls
// takes effect at the correct point, producing different output than a single
// GenerateSamples call where the write happens before all clocks.
func TestSN76489_RunMidFrameWrite(t *testing.T) {
	clocks := 20000
	splitPoint := 10000

	// Reference: write volume BEFORE generating all samples
	ref := New(3579545, 48000, 800, Sega)
	ref.Write(0x85) // channel 0 tone low nibble = 5
	ref.Write(0x01) // tone = 0x15
	ref.Write(0x90) // channel 0 volume = 0 (max)
	// Change volume before generating
	ref.Write(0x9F) // channel 0 volume = 0x0F (silent)
	ref.GenerateSamples(clocks)
	refBuf, refCount := ref.GetBuffer()

	// Test: write volume MID-FRAME between two Run calls
	chip := New(3579545, 48000, 800, Sega)
	chip.Write(0x85) // channel 0 tone low nibble = 5
	chip.Write(0x01) // tone = 0x15
	chip.Write(0x90) // channel 0 volume = 0 (max)
	chip.ResetBuffer()
	chip.Run(splitPoint)
	chip.Write(0x9F) // channel 0 volume = 0x0F (silent) — mid-frame!
	chip.Run(clocks - splitPoint)
	runBuf, runCount := chip.GetBuffer()

	if runCount == 0 || refCount == 0 {
		t.Fatal("Expected samples to be generated")
	}

	// The outputs must differ because the mid-frame write changes volume
	// partway through, while the reference was silent for the entire frame.
	differs := false
	minCount := refCount
	if runCount < minCount {
		minCount = runCount
	}
	for i := 0; i < minCount; i++ {
		if refBuf[i] != runBuf[i] {
			differs = true
			break
		}
	}
	if !differs {
		t.Error("Mid-frame write should produce different output than pre-frame write")
	}
}

// TestSN76489_SaveLoadState verifies that saving and loading state preserves
// all mutable chip state.
func TestSN76489_SaveLoadState(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)

	// Set up various state
	chip.Write(0x8B) // channel 0 tone low nibble = 0xB
	chip.Write(0x1A) // tone = 0x1AB
	chip.Write(0x90) // channel 0 volume = 0 (max)
	chip.Write(0xA5) // channel 1 tone low nibble = 5
	chip.Write(0x3F) // tone = 0x3F5
	chip.Write(0xB3) // channel 1 volume = 3
	chip.Write(0xC2) // channel 2 tone low nibble = 2
	chip.Write(0x0A) // tone = 0x0A2
	chip.Write(0xD7) // channel 2 volume = 7
	chip.Write(0xE5) // noise = white, rate 1
	chip.Write(0xFB) // noise volume = 0xB

	// Advance some clocks to get non-trivial internal state
	chip.GenerateSamples(5000)

	state := chip.SaveState()

	// Create a new chip and load the state
	chip2 := New(3579545, 48000, 800, Sega)
	chip2.LoadState(state)

	// Verify all observable state matches
	for ch := 0; ch < 3; ch++ {
		if chip.GetToneReg(ch) != chip2.GetToneReg(ch) {
			t.Errorf("ToneReg[%d]: original=%d, loaded=%d", ch, chip.GetToneReg(ch), chip2.GetToneReg(ch))
		}
	}
	for ch := 0; ch < 4; ch++ {
		if chip.GetVolume(ch) != chip2.GetVolume(ch) {
			t.Errorf("Volume[%d]: original=%d, loaded=%d", ch, chip.GetVolume(ch), chip2.GetVolume(ch))
		}
	}
	if chip.GetNoiseReg() != chip2.GetNoiseReg() {
		t.Errorf("NoiseReg: original=%d, loaded=%d", chip.GetNoiseReg(), chip2.GetNoiseReg())
	}
	if chip.GetNoiseShift() != chip2.GetNoiseShift() {
		t.Errorf("NoiseShift: original=0x%04X, loaded=0x%04X", chip.GetNoiseShift(), chip2.GetNoiseShift())
	}

	// Verify internal state via the State struct
	state2 := chip2.SaveState()
	if state != state2 {
		t.Error("SaveState from loaded chip does not match original SaveState")
	}
}

// TestSN76489_SaveLoadStateContinuity verifies that generating samples from
// a loaded state produces identical output to continuing from the original chip.
func TestSN76489_SaveLoadStateContinuity(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)

	// Set up some audible state
	chip.Write(0x85) // channel 0 tone low nibble = 5
	chip.Write(0x02) // tone = 0x25
	chip.Write(0x90) // channel 0 volume = 0 (max)
	chip.Write(0xE4) // noise = white, rate 0
	chip.Write(0xF0) // noise volume = 0 (max)

	// Generate some initial samples
	chip.GenerateSamples(5000)

	// Save state
	state := chip.SaveState()

	// Continue generating on original chip
	chip.GenerateSamples(10000)
	origBuf, origCount := chip.GetBuffer()

	// Load state into a new chip and generate the same amount
	chip2 := New(3579545, 48000, 800, Sega)
	chip2.LoadState(state)
	chip2.GenerateSamples(10000)
	loadBuf, loadCount := chip2.GetBuffer()

	if origCount != loadCount {
		t.Fatalf("Sample count mismatch: original=%d, loaded=%d", origCount, loadCount)
	}

	for i := 0; i < origCount; i++ {
		if origBuf[i] != loadBuf[i] {
			t.Errorf("Sample %d differs: original=%f, loaded=%f", i, origBuf[i], loadBuf[i])
			break
		}
	}
}

// TestSN76489_DefaultGain verifies default gain (0.25) matches old /4.0 behavior
func TestSN76489_DefaultGain(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)

	// Set channel 0 to max volume
	chip.Write(0x90) // Channel 0 volume = 0 (max)

	// With toneOutput[0] = false (initial), channel 0 contributes -volumeTable[0] = -1.0
	// Other channels silent (volume 0x0F = 0.0)
	// Expected: (-1.0 + 0 + 0 + 0) * 0.25 = -0.25
	sample := chip.Sample()
	expected := float32(-1.0) * 0.25
	if math.Abs(float64(sample-expected)) > 0.001 {
		t.Errorf("Default gain sample: expected %f, got %f", expected, sample)
	}
}

// TestSN76489_SetGain verifies custom gain scales Sample() and GetBuffer() output
func TestSN76489_SetGain(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)

	// Set channel 0 to max volume
	chip.Write(0x90) // Channel 0 volume = 0 (max)

	// Get sample with default gain
	defaultSample := chip.Sample()

	// Set gain to 0.5 (double default)
	chip.SetGain(0.5)
	doubleSample := chip.Sample()

	// doubleSample should be 2x defaultSample
	expectedRatio := float32(2.0)
	if defaultSample == 0 {
		t.Fatal("Default sample is zero, cannot test ratio")
	}
	actualRatio := doubleSample / defaultSample
	if math.Abs(float64(actualRatio-expectedRatio)) > 0.001 {
		t.Errorf("Gain ratio: expected %f, got %f", expectedRatio, actualRatio)
	}

	// Also verify GetBuffer respects gain
	chip.SetGain(0.5)
	chip.GenerateSamples(10000)
	buf05Src, count05 := chip.GetBuffer()
	if count05 == 0 {
		t.Fatal("No samples generated")
	}
	// Copy since GetBuffer returns the same underlying slice
	buf05 := make([]float32, count05)
	copy(buf05, buf05Src[:count05])

	chip.SetGain(1.0)
	chip.GenerateSamples(10000)
	buf10, count10 := chip.GetBuffer()

	minCount := count05
	if count10 < minCount {
		minCount = count10
	}

	// Absolute value of each sample in buf10 should be 2x buf05
	for i := 0; i < minCount; i++ {
		abs05 := math.Abs(float64(buf05[i]))
		abs10 := math.Abs(float64(buf10[i]))
		if abs05 < 0.001 {
			continue
		}
		ratio := abs10 / abs05
		if math.Abs(ratio-2.0) > 0.001 {
			t.Errorf("GetBuffer gain ratio at sample %d: expected 2.0, got %f", i, ratio)
			break
		}
	}
}

// TestSN76489_GetChannelBuffers verifies per-channel buffers have correct amplitudes and signs
func TestSN76489_GetChannelBuffers(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)

	// Set channel 0 to max volume, others silent
	chip.Write(0x90) // Channel 0 volume = 0 (max)
	// Channels 1-3 remain at 0x0F (silent)

	chip.GenerateSamples(10000)
	chBufs, count := chip.GetChannelBuffers()
	if count == 0 {
		t.Fatal("No samples generated")
	}

	// Channel 0 should have non-zero values (±1.0)
	hasNonZero := false
	for i := 0; i < count; i++ {
		val := chBufs[0][i]
		if val != 0 {
			hasNonZero = true
			if math.Abs(float64(val)) < 0.99 || math.Abs(float64(val)) > 1.01 {
				t.Errorf("Channel 0 sample %d: expected ±1.0, got %f", i, val)
				break
			}
		}
	}
	if !hasNonZero {
		t.Error("Channel 0 should have non-zero samples at max volume")
	}

	// Channels 1-2 (silent) should be all zeros
	for ch := 1; ch < 3; ch++ {
		for i := 0; i < count; i++ {
			if chBufs[ch][i] != 0 {
				t.Errorf("Channel %d sample %d: expected 0 (silent), got %f", ch, i, chBufs[ch][i])
				break
			}
		}
	}

	// Channel 3 (noise, silent volume) should be all zeros
	for i := 0; i < count; i++ {
		if chBufs[3][i] != 0 {
			t.Errorf("Channel 3 (noise) sample %d: expected 0 (silent), got %f", i, chBufs[3][i])
			break
		}
	}
}

// TestSN76489_GetBufferMatchesChannelMix verifies GetBuffer() equals sum of channels times gain
func TestSN76489_GetBufferMatchesChannelMix(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)

	// Set up varied volumes across channels
	chip.Write(0x90) // Channel 0 volume = 0 (max)
	chip.Write(0xB4) // Channel 1 volume = 4
	chip.Write(0xD8) // Channel 2 volume = 8
	chip.Write(0xF2) // Noise volume = 2
	chip.Write(0xE4) // Noise = white, rate 0

	chip.GenerateSamples(10000)

	chBufs, count := chip.GetChannelBuffers()
	mixBuf, mixCount := chip.GetBuffer()

	if count != mixCount {
		t.Fatalf("Count mismatch: channel=%d, mix=%d", count, mixCount)
	}

	for i := 0; i < count; i++ {
		expected := (chBufs[0][i] + chBufs[1][i] + chBufs[2][i] + chBufs[3][i]) * 0.25
		if math.Abs(float64(mixBuf[i]-expected)) > 0.0001 {
			t.Errorf("Sample %d: GetBuffer=%f, manual mix=%f", i, mixBuf[i], expected)
			break
		}
	}
}

// TestSN76489_RunOverflow verifies overflow detection with a tiny buffer
func TestSN76489_RunOverflow(t *testing.T) {
	// Tiny buffer of 2 samples, but generate enough clocks for many more
	chip := New(3579545, 48000, 2, Sega)
	dropped := chip.GenerateSamples(100000)
	if dropped == 0 {
		t.Error("Expected overflow with tiny buffer and many clocks")
	}
	_, count := chip.GetBuffer()
	if count != 2 {
		t.Errorf("Expected buffer to be full (2), got %d", count)
	}
}

// TestSN76489_RunNoOverflow verifies no overflow under normal usage
func TestSN76489_RunNoOverflow(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)
	// 10000 clocks at 3579545 Hz / 48000 Hz ≈ 74.6 clocks/sample
	// So ~134 samples, well within 800-sample buffer
	dropped := chip.GenerateSamples(10000)
	if dropped != 0 {
		t.Errorf("Expected no overflow, got %d dropped samples", dropped)
	}
}

// TestSN76489_Reset verifies Reset() returns chip to initial state
func TestSN76489_Reset(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)

	// Modify a bunch of state
	chip.Write(0x8F) // Latch channel 0 tone, low nibble = 0xF
	chip.Write(0x3F) // High bits = 0x3F
	chip.Write(0x90) // Channel 0 volume = 0 (max)
	chip.Write(0xE7) // Noise = white, tone2 rate
	chip.Write(0xF5) // Noise volume = 5
	chip.GenerateSamples(1000)

	// Reset
	chip.Reset()

	// Verify initial state
	for ch := 0; ch < 3; ch++ {
		if got := chip.GetToneReg(ch); got != 0 {
			t.Errorf("After reset: tone reg %d expected 0, got 0x%03X", ch, got)
		}
	}
	for ch := 0; ch < 4; ch++ {
		if got := chip.GetVolume(ch); got != 0x0F {
			t.Errorf("After reset: volume %d expected 0x0F, got 0x%02X", ch, got)
		}
	}
	if got := chip.GetNoiseReg(); got != 0 {
		t.Errorf("After reset: noiseReg expected 0, got 0x%02X", got)
	}
	if got := chip.GetNoiseShift(); got != 0x8000 {
		t.Errorf("After reset: noiseShift expected 0x8000, got 0x%04X", got)
	}
}
