# Camera View

Tiny low-latency viewer for a capture card (or any camera) with live audio. Built for playing an Xbox through a capture card. Works on Windows, macOS and Linux.

It's a small Go app that picks your devices and drives `ffplay`: one process shows video in a native window, another plays audio. Running them separately means neither waits on A/V sync, which keeps input lag as low as possible.

## Requirements

[FFmpeg](https://ffmpeg.org/download.html) (`ffmpeg` + `ffplay`) on your PATH:

- Windows: `winget install ffmpeg` or `choco install ffmpeg-full`
- macOS: `brew install ffmpeg`
- Linux: `sudo apt install ffmpeg` (audio uses PulseAudio/PipeWire via `pactl`)

## Use

```
go build -o camera-view .     # or camera-view.exe on Windows
./camera-view                 # first run: pick video + audio device
./camera-view -pick           # pick devices again
```

The first run lists devices. The capture card's own audio device is preselected, so press Enter to accept. Choices are saved to `camera-view/config.json` in your user config dir, and later runs start straight away.

In the window: `f` or double-click toggles fullscreen, `q` or `Esc` quits. Closing the window stops the audio too.

## Config

`config.json` fields: `video`, `audio` (`""` = none), `size` (default `1920x1080`), `fps` (default `60`), `volume` (0-100). If the card rejects the size/fps, the app falls back to the card's default mode.

## Cross-compile

```
GOOS=darwin GOARCH=arm64 go build -o camera-view-mac .
GOOS=linux  GOARCH=amd64 go build -o camera-view-linux .
GOOS=windows GOARCH=amd64 go build -o camera-view.exe .
```

## Troubleshooting

- **No sound / audio hangs:** most capture cards send no audio until the console outputs an HDMI signal. Turn the console on first.
- **"Device busy":** close OBS, the Camera app, etc. Most cards allow one reader at a time.
- **macOS:** grant camera + microphone permission to your terminal the first time.
