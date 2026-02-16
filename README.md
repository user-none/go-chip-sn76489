# go-chip-sn76489

Go emulation of the SN76489 Programmable Sound Generator, the sound chip used
in the Sega Master System, Game Gear, Genesis/Mega Drive, SG-1000, ColecoVision,
BBC Micro, and many other systems.

## Features

- 3 square wave tone channels + 1 noise channel
- TI SN76489 and Sega variants (LFSR size, tap bits, tone-zero behavior)
- Per-channel output buffers for stereo panning (Game Gear) and custom mixing
- Configurable gain for level control when mixing with other chips (Genesis YM2612)
- Cycle-accurate mid-frame register writes via `ResetBuffer`/`Run`/`Write`/`Run`
- Buffer overflow detection (dropped sample count returned from `Run`/`GenerateSamples`)
- Save/load state for snapshots and rewinding

## Install

```
go get github.com/user-none/go-chip-sn76489
```

## Usage

### SMS (mono)

```go
import "github.com/user-none/go-chip-sn76489"

chip := sn76489.New(3579545, 48000, 800, sn76489.Sega)

// Each frame:
chip.GenerateSamples(clocksThisFrame)
buf, count := chip.GetBuffer()
// Send buf[:count] to audio output.
```

### SMS (cycle-accurate mid-frame writes)

```go
chip.ResetBuffer()
chip.Run(cyclesBeforeWrite)
chip.Write(value)
chip.Run(remainingCycles)
buf, count := chip.GetBuffer()
```

### Game Gear (stereo panning)

The Game Gear stereo register (port 0x06) controls which channels go to the
left and right speakers. Use `GetChannelBuffers` to access raw per-channel
output and apply panning yourself.

```go
chip.GenerateSamples(clocks)
chBufs, count := chip.GetChannelBuffers()
for i := 0; i < count; i++ {
    for ch := 0; ch < 4; ch++ {
        if stereoReg&(0x10<<ch) != 0 {
            left[i] += chBufs[ch][i]
        }
        if stereoReg&(0x01<<ch) != 0 {
            right[i] += chBufs[ch][i]
        }
    }
}
```

### Genesis (mixing PSG with YM2612)

Use `SetGain` to control the PSG level relative to the FM output.

```go
chip := sn76489.New(3579545, 48000, 800, sn76489.Sega)
chip.SetGain(0.5) // PSG at half level relative to FM

chip.GenerateSamples(clocks)
buf, count := chip.GetBuffer()
for i := 0; i < count; i++ {
    fmOutput[i] += buf[i]
}
```

### Buffer sizing

Use `ClocksPerSample` to pre-calculate how large the buffer needs to be:

```go
samplesPerFrame := int(math.Ceil(float64(clocksPerFrame) / chip.ClocksPerSample()))
chip := sn76489.New(clockFreq, sampleRate, samplesPerFrame, sn76489.Sega)
```

### Save states

`SaveState` and `LoadState` capture all mutable chip state. Gain is host-side
audio config and is not included; persist it separately via `GetGain`/`SetGain`
if you've changed it from the default.

```go
state := chip.SaveState()
// ... serialize state however you like ...

chip2 := sn76489.New(3579545, 48000, 800, sn76489.Sega)
chip2.LoadState(state)
// chip2 now produces identical output to chip from this point forward.
```

## Chip variants

Pass a `Config` to `New` to select the chip variant:

| Config | LFSR | White noise taps | Tone reg 0 | Used in |
|---|---|---|---|---|
| `sn76489.Sega` | 16-bit | bits 0,3 | treated as 1 | SMS, Game Gear, Genesis |
| `sn76489.TI` | 15-bit | bits 0,1 | treated as 1024 | SN76489, ColecoVision, BBC Micro |

Custom variants can be constructed directly:

```go
myConfig := sn76489.Config{
    LFSRBits:       16,
    WhiteNoiseTaps: 0x0009,
    ToneZero:       sn76489.ToneZeroAsOne,
}
```

## API reference

### Construction and lifecycle

| Method | Description |
|---|---|
| `New(clockFreq, sampleRate, bufferSize, config)` | Create a new instance |
| `Reset()` | Power-on defaults (gain preserved) |

### Chip I/O

| Method | Description |
|---|---|
| `Write(value)` | Write to the chip (latch/data bytes) |
| `Clock()` | Advance one input clock cycle |

### Audio generation

| Method | Description |
|---|---|
| `GenerateSamples(clocks) int` | Reset buffer + run clocks, returns dropped count |
| `Run(clocks) int` | Accumulate samples without resetting, returns dropped count |
| `ResetBuffer()` | Reset buffer position (call before first `Run` each frame) |
| `Sample() float32` | Point-in-time mixed sample with gain (convenience) |

### Audio output

| Method | Description |
|---|---|
| `GetBuffer() ([]float32, int)` | Mono mix with gain applied |
| `GetChannelBuffers() ([4][]float32, int)` | Raw per-channel buffers, no gain |

Both return internal slices that are reused across calls. Copy the data if you
need to retain it beyond the next `GenerateSamples`/`Run`/`GetBuffer` call.

### Configuration

| Method | Description |
|---|---|
| `SetGain(gain)` | Set mix gain (default 0.25) |
| `GetGain() float32` | Read current gain |
| `ClocksPerSample() float64` | Input clocks per output sample |

### Register inspection

| Method | Description |
|---|---|
| `GetToneReg(ch) uint16` | 10-bit tone register (ch 0-2) |
| `GetVolume(ch) uint8` | 4-bit volume (ch 0-3, 0=max, 15=silent) |
| `GetNoiseReg() uint8` | Noise control register |
| `GetNoiseShift() uint16` | Current LFSR state |

### Serialization

| Method | Description |
|---|---|
| `SaveState() State` | Snapshot all mutable chip state |
| `LoadState(State)` | Restore chip state from snapshot |

## Testing

```
go test -v ./...
```

### Golden tests

The test suite includes SHA-256 hash-based golden tests that pin the exact audio
output of the emulator across both chip variants and all channel types. These are
synthetic tests (not compared against hardware captures) that guard against
unintentional regressions.

To update the golden hashes after an intentional change to the emulation:

```
go test -run Golden -update
```

This prints new expected sample values and hashes. Paste them into
`sn76489_golden_test.go`, then verify:

```
go test -v -count=1 ./...
```

## Documentation

Hardware reference documentation is in the `docs/` directory.
