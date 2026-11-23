package backend

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// Windows microphone capture with the winmm waveIn API. It needs no extra
// programs or libraries, works on every Windows version, and with the wave
// mapper Windows itself converts the device's native format (e.g. 48 kHz
// stereo) to the 16 kHz mono 16-bit PCM whisper wants.

var (
	winmm                   = syscall.NewLazyDLL("winmm.dll")
	procWaveInGetNumDevs    = winmm.NewProc("waveInGetNumDevs")
	procWaveInGetDevCapsW   = winmm.NewProc("waveInGetDevCapsW")
	procWaveInOpen          = winmm.NewProc("waveInOpen")
	procWaveInClose         = winmm.NewProc("waveInClose")
	procWaveInPrepareHeader = winmm.NewProc("waveInPrepareHeader")
	procWaveInUnprepareHdr  = winmm.NewProc("waveInUnprepareHeader")
	procWaveInAddBuffer     = winmm.NewProc("waveInAddBuffer")
	procWaveInStart         = winmm.NewProc("waveInStart")
	procWaveInStop          = winmm.NewProc("waveInStop")
	procWaveInReset         = winmm.NewProc("waveInReset")
	procWaveInMessage       = winmm.NewProc("waveInMessage")
	procWaveInGetErrorTextW = winmm.NewProc("waveInGetErrorTextW")

	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procCreateEventW = kernel32.NewProc("CreateEventW")
)

const (
	waveMapper             = 0xFFFFFFFF // WAVE_MAPPER: the default input device
	waveFormatPCM          = 1
	callbackEvent          = 0x00050000 // CALLBACK_EVENT
	waveMapped             = 0x0004     // WAVE_MAPPED: convert formats for a specific device
	whdrDone               = 0x00000001
	drvmMapperPreferredGet = 0x2015 // DRVM_MAPPER_PREFERRED_GET
	waitTimeout            = 0x00000102

	captureBuffers = 10
	captureBufMs   = 50 // like parecord's --latency-msec=50
	captureBufSize = sampleRate * 2 * captureBufMs / 1000
)

// waveInCaps is WAVEINCAPSW.
type waveInCaps struct {
	Mid           uint16
	Pid           uint16
	DriverVersion uint32
	Pname         [32]uint16
	Formats       uint32
	Channels      uint16
	Reserved1     uint16
}

// waveFormatEx is WAVEFORMATEX (cbSize = 0, so Go's tail padding is harmless).
type waveFormatEx struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	CbSize         uint16
}

// waveHdr is WAVEHDR; field order and sizes match the C layout on amd64/arm64.
type waveHdr struct {
	Data          *byte
	BufferLength  uint32
	BytesRecorded uint32
	User          uintptr
	Flags         uint32
	Loops         uint32
	Next          uintptr
	Reserved      uintptr
}

func mmError(fn string, r uintptr) error {
	var buf [256]uint16
	procWaveInGetErrorTextW.Call(r, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if msg := syscall.UTF16ToString(buf[:]); msg != "" {
		return fmt.Errorf("%s: %s", fn, msg)
	}
	return fmt.Errorf("%s: MMRESULT %d", fn, r)
}

// inputDevices returns the waveIn device names by index. waveIn names are
// cut to 31 characters by Windows; duplicates get a " (2)", " (3)" suffix so
// each name picks exactly one device.
func inputDevices() []string {
	n, _, _ := procWaveInGetNumDevs.Call()
	names := make([]string, 0, n)
	seen := map[string]int{}
	for i := uintptr(0); i < n; i++ {
		var caps waveInCaps
		if r, _, _ := procWaveInGetDevCapsW.Call(i, uintptr(unsafe.Pointer(&caps)), unsafe.Sizeof(caps)); r != 0 {
			names = append(names, fmt.Sprintf("Mikrofon %d", i+1))
			continue
		}
		name := syscall.UTF16ToString(caps.Pname[:])
		seen[name]++
		if seen[name] > 1 {
			name = fmt.Sprintf("%s (%d)", name, seen[name])
		}
		names = append(names, name)
	}
	return names
}

// preferredDevice is the index of the default recording device, or -1.
func preferredDevice() int {
	var id, status uint32
	r, _, _ := procWaveInMessage.Call(waveMapper, drvmMapperPreferredGet,
		uintptr(unsafe.Pointer(&id)), uintptr(unsafe.Pointer(&status)))
	if r != 0 || id == waveMapper {
		return -1
	}
	return int(id)
}

// listMicrophones returns the recording devices, the system default first.
func listMicrophones() ([]Microphone, error) {
	names := inputDevices()
	def := preferredDevice()
	mics := make([]Microphone, 0, len(names))
	for i, name := range names {
		m := Microphone{Name: name, Description: name, Default: i == def}
		if m.Default {
			mics = append([]Microphone{m}, mics...)
		} else {
			mics = append(mics, m)
		}
	}
	return mics, nil
}

// winCapture is a running waveIn recording. One goroutine (loop) owns the
// device: it hands filled buffers to the pipe and queues them again, and on
// Stop it releases the device and closes the pipe (→ EOF for the reader).
type winCapture struct {
	h     uintptr // HWAVEIN
	event uintptr
	hdrs  [captureBuffers]waveHdr
	bufs  [captureBuffers][captureBufSize]byte
	pw    *io.PipeWriter
	stop  chan struct{}
	done  chan struct{}
	once  sync.Once
}

func (c *winCapture) Stop()       { c.once.Do(func() { close(c.stop) }) }
func (c *winCapture) Wait() error { <-c.done; return nil }

// startCapture records 16 kHz mono s16le audio from the named device. An
// empty name, or a device that is no longer present (unplugged since it was
// chosen), records from the system default device.
func startCapture(device string, _ io.Writer) (audioCapture, io.ReadCloser, error) {
	if err := winmm.Load(); err != nil {
		return nil, nil, err
	}
	devID, flags := uintptr(waveMapper), uintptr(callbackEvent)
	if device != "" {
		for i, name := range inputDevices() {
			if name == device {
				devID, flags = uintptr(i), callbackEvent|waveMapped
				break
			}
		}
	}

	ev, _, err := procCreateEventW.Call(0, 0, 0, 0) // auto-reset, not signaled
	if ev == 0 {
		return nil, nil, fmt.Errorf("CreateEvent: %w", err)
	}
	c := &winCapture{event: ev, stop: make(chan struct{}), done: make(chan struct{})}
	wf := waveFormatEx{FormatTag: waveFormatPCM, Channels: 1, SamplesPerSec: sampleRate,
		AvgBytesPerSec: sampleRate * 2, BlockAlign: 2, BitsPerSample: 16}
	if r, _, _ := procWaveInOpen.Call(uintptr(unsafe.Pointer(&c.h)), devID,
		uintptr(unsafe.Pointer(&wf)), ev, 0, flags); r != 0 {
		syscall.CloseHandle(syscall.Handle(ev))
		return nil, nil, mmError("waveInOpen", r)
	}

	fail := func(fn string, r uintptr) (audioCapture, io.ReadCloser, error) {
		c.release()
		return nil, nil, mmError(fn, r)
	}
	for i := range c.hdrs {
		h := &c.hdrs[i]
		h.Data, h.BufferLength = &c.bufs[i][0], captureBufSize
		if r, _, _ := procWaveInPrepareHeader.Call(c.h, uintptr(unsafe.Pointer(h)), unsafe.Sizeof(*h)); r != 0 {
			return fail("waveInPrepareHeader", r)
		}
		if r, _, _ := procWaveInAddBuffer.Call(c.h, uintptr(unsafe.Pointer(h)), unsafe.Sizeof(*h)); r != 0 {
			return fail("waveInAddBuffer", r)
		}
	}
	if r, _, _ := procWaveInStart.Call(c.h); r != 0 {
		return fail("waveInStart", r)
	}

	pr, pw := io.Pipe()
	c.pw = pw
	go c.loop()
	return c, pr, nil
}

// loop delivers buffers in the order they were queued (the driver fills
// them in that order) until Stop.
func (c *winCapture) loop() {
	defer close(c.done)
	defer c.release()
	next := 0
	for {
		select {
		case <-c.stop:
			c.pw.Close()
			return
		default:
		}
		if r, _ := syscall.WaitForSingleObject(syscall.Handle(c.event), 100); r == waitTimeout {
			continue
		}
		for range c.hdrs {
			h := &c.hdrs[next]
			// The driver sets WHDR_DONE from its own thread.
			flags := atomic.LoadUint32(&h.Flags)
			if flags&whdrDone == 0 {
				break
			}
			if n := h.BytesRecorded; n > 0 {
				if _, err := c.pw.Write(c.bufs[next][:n]); err != nil {
					return // reader closed
				}
			}
			h.BytesRecorded = 0
			atomic.StoreUint32(&h.Flags, flags&^whdrDone)
			if r, _, _ := procWaveInAddBuffer.Call(c.h, uintptr(unsafe.Pointer(h)), unsafe.Sizeof(*h)); r != 0 {
				c.pw.CloseWithError(mmError("waveInAddBuffer", r))
				return
			}
			next = (next + 1) % captureBuffers
		}
	}
}

// release stops the device, takes the buffers back and closes everything.
func (c *winCapture) release() {
	if c.h != 0 {
		procWaveInStop.Call(c.h)
		procWaveInReset.Call(c.h) // marks every queued buffer done
		for i := range c.hdrs {
			procWaveInUnprepareHdr.Call(c.h, uintptr(unsafe.Pointer(&c.hdrs[i])), unsafe.Sizeof(c.hdrs[i]))
		}
		procWaveInClose.Call(c.h)
		c.h = 0
	}
	if c.event != 0 {
		syscall.CloseHandle(syscall.Handle(c.event))
		c.event = 0
	}
	if c.pw != nil {
		c.pw.CloseWithError(errors.New("ses kaydı kapatıldı"))
	}
}
