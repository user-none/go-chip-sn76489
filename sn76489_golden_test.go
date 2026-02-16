package sn76489

import (
	"crypto/sha256"
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"testing"
)

var update = flag.Bool("update", false, "print new golden hashes and return")

// hashFloat32Buffer computes SHA-256 of count float32 values from buf.
func hashFloat32Buffer(buf []float32, count int) [32]byte {
	b := make([]byte, count*4)
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(buf[i]))
	}
	return sha256.Sum256(b)
}

// compareGoldenFloat32 compares the first N samples and the full-buffer SHA-256 hash.
func compareGoldenFloat32(t *testing.T, name string, buf []float32, count int, expectedFirst []float32, expectedHash string) {
	t.Helper()

	hash := hashFloat32Buffer(buf, count)
	hashStr := fmt.Sprintf("%x", hash)

	if *update {
		fmt.Printf("=== %s ===\n", name)
		fmt.Printf("// Buffer count: %d\n", count)
		n := 32
		if count < n {
			n = count
		}
		fmt.Printf("expectedFirst := []float32{")
		for i := 0; i < n; i++ {
			if i > 0 {
				fmt.Print(", ")
			}
			if i%4 == 0 {
				fmt.Print("\n\t")
			}
			fmt.Printf("%v", buf[i])
		}
		fmt.Printf(",\n}\n")
		fmt.Printf("expectedHash := %q\n\n", hashStr)
		return
	}

	n := len(expectedFirst)
	if count < n {
		t.Fatalf("%s: buffer too short: got %d, want at least %d", name, count, n)
	}
	for i := 0; i < n; i++ {
		if buf[i] != expectedFirst[i] {
			t.Errorf("%s: sample[%d] = %v, want %v", name, i, buf[i], expectedFirst[i])
			break
		}
	}

	if hashStr != expectedHash {
		t.Errorf("%s: hash mismatch\n  got:  %s\n  want: %s", name, hashStr, expectedHash)
	}
}

// runFrame runs the chip for one NTSC frame (262 scanlines).
func runFrame(chip *SN76489) {
	clocksPerScanline := (3579545 / 60) / 262
	for i := 0; i < 262; i++ {
		chip.Run(clocksPerScanline)
	}
}

// TestGolden_SingleToneChannel verifies exact sample output for a single
// tone channel with toneReg=4 over 16 internal ticks.
func TestGolden_SingleToneChannel(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)
	chip.SetGain(1.0)
	chip.Write(0x84) // Channel 0 tone low nibble = 4
	chip.Write(0x00) // High bits = 0, toneReg = 4
	chip.Write(0x90) // Channel 0 volume = 0 (max)

	v := vol(0)
	// Period = 2*4 = 8 ticks: HIGH for 4, LOW for 4
	expected := [16]float32{
		v, v, v, v, 0, 0, 0, 0,
		v, v, v, v, 0, 0, 0, 0,
	}

	for i := 0; i < 16; i++ {
		clockInternal(chip, 1)
		got := chip.Sample()
		if got != expected[i] {
			t.Errorf("tick %d: Sample() = %f, want %f", i+1, got, expected[i])
		}
	}
}

// TestGolden_AllChannelsMixed verifies exact mixed sample values with all
// four channels contributing at different volume levels.
func TestGolden_AllChannelsMixed(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)
	chip.SetGain(1.0)

	chip.Write(0x81) // Ch0 tone = 1 (constant HIGH)
	chip.Write(0x90) // Ch0 volume = 0
	chip.Write(0xA1) // Ch1 tone = 1
	chip.Write(0xB4) // Ch1 volume = 4
	chip.Write(0xC1) // Ch2 tone = 1
	chip.Write(0xD8) // Ch2 volume = 8
	chip.Write(0xE4) // White noise, rate 0
	chip.Write(0xF2) // Noise volume = 2

	// Clock 1 tick to activate tone outputs (toneReg=1 → constant HIGH)
	clockInternal(chip, 1)

	// noiseOut = false after first LFSR shift (0x8000 bit 0 = 0)
	wantNoNoise := vol(0) + vol(4) + vol(8)
	got := chip.Sample()
	if got != wantNoNoise {
		t.Errorf("without noise: Sample() = %f, want %f", got, wantNoNoise)
	}

	// Clock until noiseOut becomes true
	for i := 0; i < 10000; i++ {
		clockInternal(chip, 1)
		if chip.noiseOut {
			break
		}
	}
	if !chip.noiseOut {
		t.Fatal("noiseOut never became true")
	}

	wantWithNoise := vol(0) + vol(4) + vol(8) + vol(2)
	got = chip.Sample()
	if got != wantWithNoise {
		t.Errorf("with noise: Sample() = %f, want %f", got, wantWithNoise)
	}
}

// TestGolden_WhiteNoiseLFSRSequence verifies exact LFSR state and noiseOut
// for 32 consecutive white noise shifts on a Sega chip.
func TestGolden_WhiteNoiseLFSRSequence(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)
	chip.Write(0xE4) // White noise, rate 0

	// Pre-compute expected values using lfsrStep
	type step struct {
		shift uint16
		out   bool
	}
	expected := make([]step, 32)
	lfsr := uint16(0x8000)
	for i := 0; i < 32; i++ {
		out := (lfsr & 1) != 0
		lfsr = lfsrStep(lfsr, true, 0x0009, 15)
		expected[i] = step{lfsr, out}
	}

	for i := 0; i < 32; i++ {
		if i == 0 {
			clockInternal(chip, 1) // First shift at tick 1
		} else {
			clockInternal(chip, 32) // Subsequent every 2*reloadVal(0x10)
		}
		if chip.noiseShift != expected[i].shift {
			t.Errorf("shift %d: noiseShift = 0x%04X, want 0x%04X", i+1, chip.noiseShift, expected[i].shift)
		}
		if chip.noiseOut != expected[i].out {
			t.Errorf("shift %d: noiseOut = %v, want %v", i+1, chip.noiseOut, expected[i].out)
		}
	}
}

// TestGolden_PeriodicNoiseLFSRSequence verifies all 16 LFSR states through
// the full periodic noise cycle and confirms return to 0x8000.
func TestGolden_PeriodicNoiseLFSRSequence(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)
	chip.Write(0xE0) // Periodic noise, rate 0

	expectedStates := [16]uint16{
		0x4000, 0x2000, 0x1000, 0x0800, 0x0400, 0x0200, 0x0100, 0x0080,
		0x0040, 0x0020, 0x0010, 0x0008, 0x0004, 0x0002, 0x0001, 0x8000,
	}
	// noiseOut is true only when pre-shift LFSR = 0x0001 (bit 0 set)
	expectedOut := [16]bool{
		false, false, false, false, false, false, false, false,
		false, false, false, false, false, false, false, true,
	}

	for i := 0; i < 16; i++ {
		if i == 0 {
			clockInternal(chip, 1)
		} else {
			clockInternal(chip, 32) // 2 * reloadVal(0x10)
		}
		if chip.noiseShift != expectedStates[i] {
			t.Errorf("shift %d: noiseShift = 0x%04X, want 0x%04X", i+1, chip.noiseShift, expectedStates[i])
		}
		if chip.noiseOut != expectedOut[i] {
			t.Errorf("shift %d: noiseOut = %v, want %v", i+1, chip.noiseOut, expectedOut[i])
		}
	}
	if chip.noiseShift != 0x8000 {
		t.Errorf("after full period: noiseShift = 0x%04X, want 0x8000", chip.noiseShift)
	}
}

// TestGolden_PCMPlayback verifies Sample() returns exactly vol(level) for
// each of the 16 volume levels with a constant-HIGH channel and gain 1.0.
func TestGolden_PCMPlayback(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)
	chip.SetGain(1.0)
	chip.Write(0x81) // Channel 0 tone = 1 (constant HIGH)

	clockInternal(chip, 1)
	if !chip.toneOutput[0] {
		t.Fatal("toneOutput[0] should be true for toneReg=1")
	}

	for level := 0; level < 16; level++ {
		chip.Write(0x90 | uint8(level))
		got := chip.Sample()
		want := vol(level)
		if got != want {
			t.Errorf("volume level %d: Sample() = %f, want %f", level, got, want)
		}
	}
}

// TestGolden_FrequencyAccuracy verifies exact toggle timing for various
// tone register values: first toggle at tick 1, second at tick N+1, third
// at tick 2N+1, and no premature toggle at tick N.
func TestGolden_FrequencyAccuracy(t *testing.T) {
	tests := []struct {
		name string
		N    uint16
	}{
		{"N=2", 2},
		{"N=3", 3},
		{"N=5", 5},
		{"N=10", 10},
		{"N=100", 100},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chip := New(3579545, 48000, 800, Sega)
			chip.Write(0x80 | uint8(tc.N&0x0F))
			chip.Write(uint8(tc.N >> 4))
			chip.Write(0x90)

			// First toggle at tick 1
			clockInternal(chip, 1)
			if !chip.toneOutput[0] {
				t.Fatal("no toggle at tick 1")
			}

			// No premature toggle at tick N
			clockInternal(chip, int(tc.N)-1)
			if !chip.toneOutput[0] {
				t.Error("premature toggle at tick N")
			}

			// Second toggle at tick N+1
			clockInternal(chip, 1)
			if chip.toneOutput[0] {
				t.Error("expected second toggle at tick N+1")
			}

			// Third toggle at tick 2N+1
			clockInternal(chip, int(tc.N))
			if !chip.toneOutput[0] {
				t.Error("expected third toggle at tick 2N+1")
			}
		})
	}
}

// TestGolden_TwoToneMixedSequence verifies exact mixed output for two tone
// channels at different frequencies over 12 internal ticks.
// Ch0 toneReg=2 (period 4): T,T,F,F,T,T,F,F,T,T,F,F
// Ch1 toneReg=3 (period 6): T,T,T,F,F,F,T,T,T,F,F,F
func TestGolden_TwoToneMixedSequence(t *testing.T) {
	chip := New(3579545, 48000, 800, Sega)
	chip.SetGain(1.0)
	chip.Write(0x82) // Ch0 tone = 2
	chip.Write(0x90) // Ch0 volume = 0 (max)
	chip.Write(0xA3) // Ch1 tone = 3
	chip.Write(0xB4) // Ch1 volume = 4

	v0 := vol(0)
	v4 := vol(4)
	expected := [12]float32{
		v0 + v4, v0 + v4, v4, 0, v0, v0,
		v4, v4, v0 + v4, v0, 0, 0,
	}

	for i := 0; i < 12; i++ {
		clockInternal(chip, 1)
		got := chip.Sample()
		if got != expected[i] {
			t.Errorf("tick %d: Sample() = %f, want %f", i+1, got, expected[i])
		}
	}
}

// TestGolden_TIvsSegaLFSR verifies that TI and Sega white noise LFSRs
// produce different sequences and that TI never exceeds 15 bits.
func TestGolden_TIvsSegaLFSR(t *testing.T) {
	sega := New(3579545, 48000, 800, Sega)
	sega.Write(0xE4) // White noise, rate 0

	ti := New(3579545, 48000, 800, TI)
	ti.Write(0xE4) // White noise, rate 0

	segaStates := make([]uint16, 5)
	tiStates := make([]uint16, 5)

	for i := 0; i < 5; i++ {
		if i == 0 {
			clockInternal(sega, 1)
			clockInternal(ti, 1)
		} else {
			clockInternal(sega, 32)
			clockInternal(ti, 32)
		}
		segaStates[i] = sega.noiseShift
		tiStates[i] = ti.noiseShift

		if tiStates[i] > 0x7FFF {
			t.Errorf("shift %d: TI LFSR = 0x%04X exceeds 15 bits", i+1, tiStates[i])
		}
	}

	diverged := false
	for i := 0; i < 5; i++ {
		if segaStates[i] != tiStates[i] {
			diverged = true
			break
		}
	}
	if !diverged {
		t.Error("Sega and TI LFSR states should diverge")
	}
}

// --- SHA-256 buffer golden tests ---

// TestGolden_DefaultGainSingleTone verifies default gain (0.25) with a single
// tone channel through the buffer output path (Run/GetBuffer).
func TestGolden_DefaultGainSingleTone(t *testing.T) {
	chip := New(3579545, 48000, 1024, Sega)
	// No SetGain call — use default 0.25

	// Ch0: ~440Hz (toneReg=254), vol=0 (max)
	chip.Write(0x8E) // Latch ch0 tone, low4=0xE
	chip.Write(0x0F) // high6=0x0F -> toneReg=254
	chip.Write(0x90) // Ch0 vol=0 (max)

	// Silence other channels
	chip.Write(0xBF) // Ch1 vol=15
	chip.Write(0xDF) // Ch2 vol=15
	chip.Write(0xFF) // Noise vol=15

	chip.ResetBuffer()
	runFrame(chip)
	buf, count := chip.GetBuffer()

	expectedFirst := []float32{
		0.25, 0.25, 0.25, 0.25,
		0.25, 0.25, 0.25, 0.25,
		0.25, 0.25, 0.25, 0.25,
		0.25, 0.25, 0.25, 0.25,
		0.25, 0.25, 0.25, 0.25,
		0.25, 0.25, 0.25, 0.25,
		0.25, 0.25, 0.25, 0.25,
		0.25, 0.25, 0.25, 0.25,
	}
	expectedHash := "ef02974bd31204e10fa664c15e74953c99f114fb7b90c6e9f945cef1a25bbc96"

	compareGoldenFloat32(t, "DefaultGainSingleTone", buf, count, expectedFirst, expectedHash)
}

// TestGolden_ThreeTonesBuffer verifies 3 simultaneous tone channels at
// different frequencies through the buffer output path.
func TestGolden_ThreeTonesBuffer(t *testing.T) {
	chip := New(3579545, 48000, 1024, Sega)
	// No SetGain — default 0.25

	// Ch0: ~440Hz (toneReg=254), vol=0
	chip.Write(0x8E)
	chip.Write(0x0F)
	chip.Write(0x90)

	// Ch1: ~880Hz (toneReg=127), vol=4
	chip.Write(0xAF) // Latch ch1 tone, low4=0xF
	chip.Write(0x07) // high6=0x07 -> toneReg=127
	chip.Write(0xB4) // Ch1 vol=4

	// Ch2: ~220Hz (toneReg=508), vol=8
	chip.Write(0xCC) // Latch ch2 tone, low4=0xC
	chip.Write(0x1F) // high6=0x1F -> toneReg=508
	chip.Write(0xD8) // Ch2 vol=8

	// Silence noise
	chip.Write(0xFF)

	chip.ResetBuffer()
	runFrame(chip)
	buf, count := chip.GetBuffer()

	expectedFirst := []float32{
		0.38914913, 0.38914913, 0.38914913, 0.38914913,
		0.38914913, 0.38914913, 0.38914913, 0.38914913,
		0.38914913, 0.38914913, 0.38914913, 0.38914913,
		0.38914913, 0.38914913, 0.38914913, 0.38914913,
		0.38914913, 0.38914913, 0.38914913, 0.38914913,
		0.38914913, 0.38914913, 0.38914913, 0.38914913,
		0.38914913, 0.38914913, 0.38914913, 0.28962234,
		0.28962234, 0.28962234, 0.28962234, 0.28962234,
	}
	expectedHash := "81ca61a1aa682a6964902ad6d01e83dc97ad569266027269a4088edd4fb10baa"

	compareGoldenFloat32(t, "ThreeTonesBuffer", buf, count, expectedFirst, expectedHash)
}

// TestGolden_WhiteNoiseBuffer verifies white noise LFSR producing actual
// sample values through the buffer output path.
func TestGolden_WhiteNoiseBuffer(t *testing.T) {
	chip := New(3579545, 48000, 1024, Sega)

	// Silence tone channels
	chip.Write(0x9F)
	chip.Write(0xBF)
	chip.Write(0xDF)

	// White noise, rate=2, vol=0 (max)
	chip.Write(0xE6) // White noise (bit2=1), rate=2
	chip.Write(0xF0) // Noise vol=0

	chip.ResetBuffer()
	runFrame(chip)
	buf, count := chip.GetBuffer()

	expectedFirst := []float32{
		0, 0, 0, 0,
		0, 0, 0, 0,
		0, 0, 0, 0,
		0, 0, 0, 0,
		0, 0, 0, 0,
		0, 0, 0, 0,
		0, 0, 0, 0,
		0, 0, 0, 0,
	}
	expectedHash := "6c9bb6c5d0395e6f5e748587c1e5f3967f717ef309dd45a686194f57b999114d"

	compareGoldenFloat32(t, "WhiteNoiseBuffer", buf, count, expectedFirst, expectedHash)
}

// TestGolden_PeriodicNoiseBuffer verifies periodic noise sample values
// through the buffer output path.
func TestGolden_PeriodicNoiseBuffer(t *testing.T) {
	chip := New(3579545, 48000, 1024, Sega)

	// Silence tone channels
	chip.Write(0x9F)
	chip.Write(0xBF)
	chip.Write(0xDF)

	// Periodic noise, rate=1, vol=0 (max)
	chip.Write(0xE1) // Periodic, rate=1
	chip.Write(0xF0) // Noise vol=0

	chip.ResetBuffer()
	runFrame(chip)
	buf, count := chip.GetBuffer()

	expectedFirst := []float32{
		0, 0, 0, 0,
		0, 0, 0, 0,
		0, 0, 0, 0,
		0, 0, 0, 0,
		0, 0, 0, 0,
		0, 0, 0, 0,
		0, 0, 0, 0,
		0, 0, 0, 0,
	}
	expectedHash := "5eb9616c5fe255ad05a8f10fd282007144814854c3bc19cfd28651c19d35d60c"

	compareGoldenFloat32(t, "PeriodicNoiseBuffer", buf, count, expectedFirst, expectedHash)
}

// TestGolden_NoiseRate3Tone2Buffer verifies noise rate 3 (tone2-driven)
// sample output through the buffer path.
func TestGolden_NoiseRate3Tone2Buffer(t *testing.T) {
	chip := New(3579545, 48000, 1024, Sega)

	// Ch0, Ch1 silent
	chip.Write(0x9F)
	chip.Write(0xBF)

	// Ch2: ~440Hz (toneReg=254), vol=4 (audible)
	chip.Write(0xCE)
	chip.Write(0x0F)
	chip.Write(0xD4)

	// Noise: rate=3 (tone2-driven), white, vol=0 (max)
	chip.Write(0xE7) // White noise (bit2=1), rate=3
	chip.Write(0xF0) // Noise vol=0

	chip.ResetBuffer()
	runFrame(chip)
	buf, count := chip.GetBuffer()

	expectedFirst := []float32{
		0.09952679, 0.09952679, 0.09952679, 0.09952679,
		0.09952679, 0.09952679, 0.09952679, 0.09952679,
		0.09952679, 0.09952679, 0.09952679, 0.09952679,
		0.09952679, 0.09952679, 0.09952679, 0.09952679,
		0.09952679, 0.09952679, 0.09952679, 0.09952679,
		0.09952679, 0.09952679, 0.09952679, 0.09952679,
		0.09952679, 0.09952679, 0.09952679, 0.09952679,
		0.09952679, 0.09952679, 0.09952679, 0.09952679,
	}
	expectedHash := "7dc48d7c56b55b68e2a994c53f638d6fb452d16dc3f6f5fb329fe6c3bfbe6d44"

	compareGoldenFloat32(t, "NoiseRate3Tone2Buffer", buf, count, expectedFirst, expectedHash)
}

// TestGolden_TIVariantBuffer verifies TI variant behavior where toneReg=0
// maps to 1024 (toggles normally, unlike Sega constant HIGH).
func TestGolden_TIVariantBuffer(t *testing.T) {
	chip := New(3579545, 48000, 1024, TI)

	// Ch0: toneReg=0 (maps to 1024 on TI), vol=0 (max)
	chip.Write(0x80) // Latch ch0 tone, low4=0
	chip.Write(0x00) // high6=0 -> toneReg=0
	chip.Write(0x90) // Ch0 vol=0

	// Silence other channels
	chip.Write(0xBF)
	chip.Write(0xDF)
	chip.Write(0xFF)

	chip.ResetBuffer()
	runFrame(chip)
	buf, count := chip.GetBuffer()

	expectedFirst := []float32{
		0.25, 0.25, 0.25, 0.25,
		0.25, 0.25, 0.25, 0.25,
		0.25, 0.25, 0.25, 0.25,
		0.25, 0.25, 0.25, 0.25,
		0.25, 0.25, 0.25, 0.25,
		0.25, 0.25, 0.25, 0.25,
		0.25, 0.25, 0.25, 0.25,
		0.25, 0.25, 0.25, 0.25,
	}
	expectedHash := "3fc4df0a3d38a384cf286efb47409f8a16668c2d7e57600e76b59de48247f5b6"

	compareGoldenFloat32(t, "TIVariantBuffer", buf, count, expectedFirst, expectedHash)
}
