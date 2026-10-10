// Camera View: low-latency viewer for a capture card (or any camera) with live audio.
// Picks devices, then drives ffplay: one process for video (native window), one for audio.
// Separate processes means neither waits on A/V sync, which keeps latency lowest for gaming.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

type Config struct {
	Video  string `json:"video"`
	Audio  string `json:"audio"`  // "" = no audio
	Size   string `json:"size"`   // e.g. "1920x1080", "" = device default
	FPS    string `json:"fps"`    // e.g. "60", "" = device default
	Volume int    `json:"volume"` // 0-100
}

type Device struct{ Label, Value string }

var stdin = bufio.NewReader(os.Stdin)

func main() {
	pick := flag.Bool("pick", false, "choose devices again")
	flag.Parse()
	if err := run(*pick); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		fmt.Print("Press Enter to exit.")
		stdin.ReadString('\n') // keep a double-clicked console window open
		os.Exit(1)
	}
}

func run(pick bool) error {
	for _, bin := range []string{"ffmpeg", "ffplay"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("%s not found on PATH; install FFmpeg (winget install ffmpeg / brew install ffmpeg / apt install ffmpeg)", bin)
		}
	}
	path := configPath()
	cfg := Config{Size: "1920x1080", FPS: "60", Volume: 100}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("bad config %s: %w", path, err)
		}
	}
	if pick || cfg.Video == "" {
		video, audio := listDevices()
		if len(video) == 0 {
			return errors.New("no video devices found")
		}
		cfg.Video = choose("Video device", video, 1, false)
		cfg.Audio = choose("Audio device", audio, matchAudio(cfg.Video, audio), true)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		b, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.WriteFile(path, b, 0o644); err != nil {
			return err
		}
		fmt.Println("Saved to", path, "(run with -pick to change devices)")
	}
	fmt.Println("Playing", cfg.Video, "| keys in window: f = fullscreen, q = quit")
	return play(cfg)
}

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "camera-view", "config.json")
}

// choose prints a numbered menu and returns the picked Value ("" for none).
// def is the option Enter selects.
func choose(title string, devs []Device, def int, allowNone bool) string {
	fmt.Println("\n" + title + ":")
	if allowNone {
		fmt.Println("  0) none")
	}
	for i, d := range devs {
		fmt.Printf("  %d) %s\n", i+1, d.Label)
	}
	for {
		fmt.Printf("Choice [%d]: ", def)
		line, err := stdin.ReadString('\n')
		line = strings.TrimSpace(line)
		n := def
		if line != "" {
			n, _ = strconv.Atoi(line)
		}
		if n == 0 && allowNone {
			return ""
		}
		if n >= 1 && n <= len(devs) {
			return devs[n-1].Value
		}
		if err != nil { // stdin closed, don't loop forever
			os.Exit(1)
		}
	}
}

// matchAudio returns the 1-based index of the audio device belonging to the video device
// (capture cards name it e.g. "Digital Audio Interface (ezcap U3 capture)"), else 0.
func matchAudio(video string, audio []Device) int {
	for i, a := range audio {
		if strings.Contains(strings.ToLower(a.Label), strings.ToLower(video)) {
			return i + 1
		}
	}
	return 0
}

func listDevices() (video, audio []Device) {
	switch runtime.GOOS {
	case "windows":
		out, _ := exec.Command("ffmpeg", "-hide_banner", "-list_devices", "true", "-f", "dshow", "-i", "dummy").CombinedOutput()
		return parseDshow(string(out))
	case "darwin":
		out, _ := exec.Command("ffmpeg", "-hide_banner", "-f", "avfoundation", "-list_devices", "true", "-i", "").CombinedOutput()
		return parseAVFoundation(string(out))
	default: // linux: v4l2 video, PulseAudio/PipeWire audio
		paths, _ := filepath.Glob("/dev/video*")
		for _, p := range paths {
			name, _ := os.ReadFile("/sys/class/video4linux/" + filepath.Base(p) + "/name")
			video = append(video, Device{p + " (" + strings.TrimSpace(string(name)) + ")", p})
		}
		if out, err := exec.Command("pactl", "list", "short", "sources").Output(); err == nil {
			audio = parsePactl(string(out))
		}
		return
	}
}

var dshowRe = regexp.MustCompile(`"([^"]+)" \(([a-z, ]+)\)`)

func parseDshow(out string) (video, audio []Device) {
	for _, m := range dshowRe.FindAllStringSubmatch(out, -1) {
		d := Device{m[1], m[1]}
		if strings.Contains(m[2], "video") {
			video = append(video, d)
		}
		if strings.Contains(m[2], "audio") {
			audio = append(audio, d)
		}
	}
	return
}

var avfRe = regexp.MustCompile(`\] \[\d+\] (.+)$`)

func parseAVFoundation(out string) (video, audio []Device) {
	var cur *[]Device
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.Contains(line, "video devices:"):
			cur = &video
		case strings.Contains(line, "audio devices:"):
			cur = &audio
		case cur != nil:
			if m := avfRe.FindStringSubmatch(line); m != nil {
				*cur = append(*cur, Device{m[1], m[1]})
			}
		}
	}
	return
}

func parsePactl(out string) (audio []Device) {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) > 1 && !strings.HasSuffix(f[1], ".monitor") { // .monitor = speaker loopback
			audio = append(audio, Device{f[1], f[1]})
		}
	}
	return
}

// ponytail: device names containing ':' break dshow/avfoundation input syntax; escape if one ever shows up.
func videoInput(dev string, mode []string) []string {
	switch runtime.GOOS {
	case "windows":
		return append(append([]string{"-f", "dshow", "-rtbufsize", "64M"}, mode...), "-i", "video="+dev)
	case "darwin":
		return append(append([]string{"-f", "avfoundation"}, mode...), "-i", dev)
	default:
		return append(append([]string{"-f", "v4l2"}, mode...), "-i", dev)
	}
}

func audioInput(dev string) []string {
	switch runtime.GOOS {
	case "windows":
		return []string{"-f", "dshow", "-audio_buffer_size", "20", "-i", "audio=" + dev} // 20ms vs ~500ms default
	case "darwin":
		return []string{"-f", "avfoundation", "-i", ":" + dev}
	default:
		return []string{"-f", "pulse", "-i", dev}
	}
}

var lowLatency = []string{"-hide_banner", "-loglevel", "error", "-fflags", "nobuffer", "-flags", "low_delay",
	"-framedrop", "-probesize", "32", "-analyzeduration", "0", "-sync", "ext"}

func play(cfg Config) error {
	if cfg.Audio != "" {
		args := append(append([]string{}, lowLatency...), "-nodisp", "-vn", "-volume", strconv.Itoa(cfg.Volume))
		audio := exec.Command("ffplay", append(args, audioInput(cfg.Audio)...)...)
		audio.Stderr = os.Stderr
		if err := audio.Start(); err != nil {
			return err
		}
		defer audio.Process.Kill() // closing the video window ends audio too
	}
	err := playVideo(cfg, true)
	if err != nil && (cfg.Size != "" || cfg.FPS != "") {
		fmt.Printf("Device rejected %s@%s, retrying with its default mode\n", cfg.Size, cfg.FPS)
		err = playVideo(cfg, false)
	}
	return err
}

func playVideo(cfg Config, withMode bool) error {
	var mode []string
	if withMode && cfg.Size != "" {
		mode = append(mode, "-video_size", cfg.Size)
	}
	if withMode && cfg.FPS != "" {
		mode = append(mode, "-framerate", cfg.FPS)
	}
	args := append(append([]string{}, lowLatency...), "-an", "-window_title", "Camera View")
	cmd := exec.Command("ffplay", append(args, videoInput(cfg.Video, mode)...)...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
