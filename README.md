# Camera View

Tiny low-latency viewer for a capture card (or any camera) with live audio. Built for playing an Xbox through a capture card. Works on Windows, macOS and Linux.

It's a small Go app: ffmpeg decodes the card into the app's own window (built with [Gio](https://gioui.org)), and a separate `ffplay` process plays the audio. Neither waits on A/V sync, which keeps input lag as low as possible.

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

In the window, move the mouse to show the top bar (it hides after 2s):

- **Settings**: live brightness, contrast, saturation and gamma sliders, plus **Expand limited range**. Turn that on if blacks look grey and the picture looks washed out (the Xbox sends TV-range video but the card labels it full range). On Windows, **Capture card settings…** opens the card driver's own dialog. **Change camera** lists video devices; click one to switch (saved for next launch).
- **Fullscreen**: also `F`. `Esc` leaves fullscreen.

Closing the window stops the audio too. Color settings are saved when you close.

## Config

`config.json` fields: `video`, `audio` (`""` = none), `size` (default `1920x1080`), `fps` (default `60`), `volume` (0-100), and the color settings from the Settings panel. If the card rejects the size/fps, the app falls back to the card's default mode.

## Building on each OS

Windows builds need only Go (Gio is pure Go there), and you can cross-compile a Windows exe from anywhere:

```
GOOS=windows GOARCH=amd64 go build -o camera-view.exe .
```

On macOS and Linux, Gio needs cgo, so build on that machine with a C compiler. Xcode command line tools on macOS. On Linux, `gcc` plus Gio's [Linux deps](https://gioui.org/doc/install/linux) (`libwayland-dev libx11-dev libx11-xcb-dev libxkbcommon-x11-dev libgles2-mesa-dev libegl1-mesa-dev libffi-dev libxcursor-dev libvulkan-dev`).

## Troubleshooting

- **No sound / audio hangs:** most capture cards send no audio until the console outputs an HDMI signal. Turn the console on first.
- **"Device busy":** close OBS, the Camera app, etc. Most cards allow one reader at a time.
- **macOS:** grant camera + microphone permission to your terminal the first time.
