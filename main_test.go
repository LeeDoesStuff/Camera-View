package main

import "testing"

func TestParsers(t *testing.T) {
	dshow := `[dshow @ 0000026fcf455e00] "ezcap U3 capture" (video)
[dshow @ 0000026fcf455e00]   Alternative name "@device_pnp_\\?\usb#vid_1bcf&pid_2c99&mi_00#7&3acf92ce&0&0000#{65e8773d}\global"
[dshow @ 0000026fcf455e00] "Digital Audio Interface (ezcap U3 capture)" (audio)
[dshow @ 0000026fcf455e00]   Alternative name "@device_cm_{33D9A762}\wave_{1050CF1F}"
[dshow @ 0000026fcf455e00] "Combo Cam" (audio, video)
`
	v, a := parseDshow(dshow)
	if len(v) != 2 || v[0].Value != "ezcap U3 capture" || v[1].Value != "Combo Cam" {
		t.Fatalf("dshow video: %v", v)
	}
	if len(a) != 2 || a[0].Value != "Digital Audio Interface (ezcap U3 capture)" {
		t.Fatalf("dshow audio: %v", a)
	}
	if matchAudio("ezcap U3 capture", a) != 1 || matchAudio("Nope", a) != 0 {
		t.Fatal("matchAudio")
	}

	avf := "[AVFoundation indev @ 0x7f] AVFoundation video devices:\r\n" +
		"[AVFoundation indev @ 0x7f] [0] FaceTime HD Camera\r\n" +
		"[AVFoundation indev @ 0x7f] [1] Capture screen 0\r\n" +
		"[AVFoundation indev @ 0x7f] AVFoundation audio devices:\r\n" +
		"[AVFoundation indev @ 0x7f] [0] USB3.0 Capture\r\n" +
		"[in#0 @ 0x7f] Error opening input: Input/output error\n"
	v, a = parseAVFoundation(avf)
	if len(v) != 2 || v[0].Value != "FaceTime HD Camera" || len(a) != 1 || a[0].Value != "USB3.0 Capture" {
		t.Fatalf("avfoundation: %v %v", v, a)
	}

	pa := "1\talsa_output.pci.monitor\tPipeWire\ts32le 2ch 48000Hz\tSUSPENDED\n" +
		"2\talsa_input.usb-capture\tPipeWire\ts16le 2ch 48000Hz\tSUSPENDED\n"
	if a = parsePactl(pa); len(a) != 1 || a[0].Value != "alsa_input.usb-capture" {
		t.Fatalf("pactl: %v", a)
	}
}

func TestAdjust(t *testing.T) {
	base := Config{Contrast: 1, Saturation: 1, Gamma: 1}
	if !newAdjust(base).noop {
		t.Fatal("defaults should be a no-op")
	}
	lim := base
	lim.LimitedRange = true
	if a := newAdjust(lim); a.lut[16] != 0 || a.lut[235] != 255 || a.lut[0] != 0 {
		t.Fatalf("limited range: 16->%d 235->%d", a.lut[16], a.lut[235])
	}
	grey := base
	grey.Saturation = 0
	pix := make([]byte, 4*1000)
	for i := range pix {
		pix[i] = byte(i * 7)
	}
	newAdjust(grey).apply(pix)
	for i := 0; i < len(pix); i += 4 {
		if pix[i] != pix[i+1] || pix[i+1] != pix[i+2] {
			t.Fatalf("saturation 0 not grey at %d: %v", i, pix[i:i+3])
		}
	}
}
