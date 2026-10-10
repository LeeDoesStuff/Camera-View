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
