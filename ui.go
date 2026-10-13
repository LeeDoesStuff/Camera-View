package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// Adjust is a precomputed color correction: a per-channel lookup table
// (range, brightness, contrast, gamma) plus saturation.
type Adjust struct {
	lut  [256]byte
	sat  int32 // saturation * 256
	noop bool
}

func newAdjust(c Config) *Adjust {
	a := &Adjust{sat: int32(math.Round(c.Saturation * 256))}
	a.noop = a.sat == 256
	for i := range a.lut {
		v := float64(i) / 255
		if c.LimitedRange { // stretch TV range 16-235 to full 0-255
			v = (v - 16.0/255) * 255 / 219
		}
		v = (v-0.5)*c.Contrast + 0.5 + c.Brightness
		v = math.Pow(math.Min(math.Max(v, 0), 1), 1/c.Gamma)
		a.lut[i] = uint8(math.Round(v * 255))
		a.noop = a.noop && int(a.lut[i]) == i
	}
	return a
}

// apply corrects RGBA pixels in place, split across CPUs.
func (a *Adjust) apply(pix []byte) {
	if a.noop {
		return
	}
	n := runtime.NumCPU()
	chunk := (len(pix)/4 + n - 1) / n * 4
	var wg sync.WaitGroup
	for off := 0; off < len(pix); off += chunk {
		part := pix[off:min(off+chunk, len(pix))]
		wg.Add(1)
		go func() { defer wg.Done(); a.applyPart(part) }()
	}
	wg.Wait()
}

func (a *Adjust) applyPart(p []byte) {
	lut, s := &a.lut, a.sat
	for i := 0; i+3 < len(p); i += 4 {
		r, g, b := int32(lut[p[i]]), int32(lut[p[i+1]]), int32(lut[p[i+2]])
		if s != 256 {
			l := (r*77 + g*150 + b*29) >> 8
			r, g, b = clamp8(l+(r-l)*s>>8), clamp8(l+(g-l)*s>>8), clamp8(l+(b-l)*s>>8)
		}
		p[i], p[i+1], p[i+2] = byte(r), byte(g), byte(b)
	}
}

func clamp8(v int32) int32 { return min(max(v, 0), 255) }

// Capture runs ffmpeg, which decodes the device to raw RGBA frames on stdout.
type Capture struct {
	cfg           Config
	win           *app.Window
	width, height int
	adjust        atomic.Pointer[Adjust]
	latest        atomic.Pointer[image.RGBA] // newest frame not yet taken by the UI
	free          chan *image.RGBA           // recycled frame buffers
	status        atomic.Pointer[string]

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func newCapture(cfg Config, win *app.Window) *Capture {
	c := &Capture{cfg: cfg, win: win, width: 1920, height: 1080, free: make(chan *image.RGBA, 3)}
	fmt.Sscanf(cfg.Size, "%dx%d", &c.width, &c.height)
	c.adjust.Store(newAdjust(cfg))
	c.setStatus("Waiting for video…")
	return c
}

func (c *Capture) setStatus(s string) { c.status.Store(&s); c.win.Invalidate() }

// Start (re)starts capture. dialog opens the driver's settings dialog first (Windows only).
func (c *Capture) Start(video string, dialog bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopLocked()
	c.cfg.Video = video
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.cancel, c.done = cancel, done
	go func() {
		defer close(done)
		frames, err := c.run(ctx, dialog, true)
		if frames == 0 && ctx.Err() == nil && (c.cfg.Size != "" || c.cfg.FPS != "") {
			c.setStatus("Device rejected " + c.cfg.Size + "@" + c.cfg.FPS + ", trying its default mode…")
			frames, err = c.run(ctx, false, false)
		}
		if ctx.Err() == nil {
			c.setStatus(fmt.Sprintf("Capture stopped: %v", err))
		}
	}()
}

func (c *Capture) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopLocked()
}

func (c *Capture) stopLocked() {
	if c.cancel != nil {
		c.cancel()
		<-c.done // device must be released before it can be reopened
		c.cancel = nil
	}
}

func (c *Capture) run(ctx context.Context, dialog, withMode bool) (frames int, err error) {
	var mode []string
	if withMode && c.cfg.Size != "" {
		mode = append(mode, "-video_size", c.cfg.Size)
	}
	if withMode && c.cfg.FPS != "" {
		mode = append(mode, "-framerate", c.cfg.FPS)
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-fflags", "nobuffer", "-flags", "low_delay",
		"-probesize", "32", "-analyzeduration", "0"}
	if dialog {
		args = append(args, "-show_video_device_dialog", "true")
	}
	args = append(args, videoInput(c.cfg.Video, mode)...)
	// ponytail: output is always scaled to the configured size so the frame size is known up front
	args = append(args, "-an", "-vf", fmt.Sprintf("scale=%d:%d", c.width, c.height),
		"-pix_fmt", "rgba", "-f", "rawvideo", "-")
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	setupProc(cmd)
	cmd.Cancel = func() error { return killTree(cmd) }
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	for {
		buf := c.getBuf()
		if _, err = io.ReadFull(out, buf.Pix); err != nil {
			c.putBuf(buf)
			break
		}
		if frames == 0 {
			c.setStatus("")
		}
		c.adjust.Load().apply(buf.Pix)
		if old := c.latest.Swap(buf); old != nil {
			c.putBuf(old) // UI never took it; drop the stale frame
		}
		c.win.Invalidate()
		frames++
	}
	cmd.Wait()
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		err = errors.New(msg[strings.LastIndex(msg, "\n")+1:])
	}
	return frames, err
}

func (c *Capture) getBuf() *image.RGBA {
	select {
	case b := <-c.free:
		return b
	default:
		return image.NewRGBA(image.Rect(0, 0, c.width, c.height))
	}
}

func (c *Capture) putBuf(b *image.RGBA) {
	select {
	case c.free <- b:
	default:
	}
}

type slider struct {
	name     string
	f        *widget.Float
	v        *float64
	min, max float64
}

type UI struct {
	cfg   *Config
	cap   *Capture
	win   *app.Window
	th    *material.Theme
	shown *image.RGBA

	settingsBtn, fullscreenBtn, resetBtn, deviceBtn, cameraBtn widget.Clickable
	cams                                                       atomic.Pointer[[]Device] // nil while listing
	camBtns                                                    []widget.Clickable
	picking                                                    bool
	bright, contrast, sat, gamma                               widget.Float
	limited                                                    widget.Bool
	sliders                                                    []slider
	open, fullscreen                                           bool
	lastMove                                                   time.Time
}

func runUI(cfg *Config, path string) error {
	var win app.Window
	win.Option(app.Title("Camera View"), app.Size(unit.Dp(1280), unit.Dp(720)))
	th := material.NewTheme()
	th.Palette.Fg = color.NRGBA{R: 230, G: 230, B: 230, A: 255}
	th.Palette.Bg = color.NRGBA{R: 20, G: 20, B: 20, A: 255}
	th.TextSize = unit.Sp(14)
	u := &UI{cfg: cfg, win: &win, th: th, cap: newCapture(*cfg, &win)}
	u.sliders = []slider{
		{"Brightness", &u.bright, &cfg.Brightness, -0.5, 0.5},
		{"Contrast", &u.contrast, &cfg.Contrast, 0.5, 2},
		{"Saturation", &u.sat, &cfg.Saturation, 0, 2},
		{"Gamma", &u.gamma, &cfg.Gamma, 0.5, 2},
	}
	u.syncWidgets()
	u.cap.Start(cfg.Video, false)
	defer u.cap.Stop()

	var ops op.Ops
	for {
		switch e := win.Event().(type) {
		case app.DestroyEvent:
			saveConfig(path, *cfg)
			return e.Err
		case app.ConfigEvent:
			u.fullscreen = e.Config.Mode == app.Fullscreen
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			u.layout(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

func (u *UI) syncWidgets() {
	for _, s := range u.sliders {
		s.f.Value = float32((*s.v - s.min) / (s.max - s.min))
	}
	u.limited.Value = u.cfg.LimitedRange
}

func (u *UI) toggleFullscreen() {
	if u.fullscreen {
		u.win.Option(app.Windowed.Option())
	} else {
		u.win.Option(app.Fullscreen.Option())
	}
}

func (u *UI) update(gtx layout.Context) {
	if u.settingsBtn.Clicked(gtx) {
		u.open = !u.open
	}
	if u.fullscreenBtn.Clicked(gtx) {
		u.toggleFullscreen()
	}
	if u.deviceBtn.Clicked(gtx) {
		u.cap.setStatus("Opening device settings…")
		go u.cap.Start(u.cfg.Video, true)
	}
	if u.cameraBtn.Clicked(gtx) {
		u.picking = !u.picking
		if u.picking {
			u.cams.Store(nil)
			go func() { // listing shells out to ffmpeg, keep it off the UI goroutine
				video, _ := listDevices()
				u.cams.Store(&video)
				u.win.Invalidate()
			}()
		}
	}
	if cams := u.cams.Load(); cams != nil {
		if len(u.camBtns) != len(*cams) {
			u.camBtns = make([]widget.Clickable, len(*cams))
		}
		for i := range u.camBtns {
			if u.camBtns[i].Clicked(gtx) {
				d := (*cams)[i]
				u.cfg.Video, u.picking = d.Value, false
				u.cap.setStatus("Switching to " + d.Label + "…")
				go u.cap.Start(d.Value, false)
			}
		}
	}
	changed := false
	if u.resetBtn.Clicked(gtx) {
		u.cfg.Brightness, u.cfg.Contrast, u.cfg.Saturation, u.cfg.Gamma, u.cfg.LimitedRange = 0, 1, 1, 1, false
		u.syncWidgets()
		changed = true
	}
	for _, s := range u.sliders {
		if s.f.Update(gtx) {
			*s.v = math.Round((s.min+float64(s.f.Value)*(s.max-s.min))*100) / 100
			changed = true
		}
	}
	if u.limited.Update(gtx) {
		u.cfg.LimitedRange = u.limited.Value
		changed = true
	}
	if changed {
		u.cap.adjust.Store(newAdjust(*u.cfg))
	}
	for {
		ev, ok := gtx.Event(key.Filter{Name: "F"}, key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			if e.Name == "F" || u.fullscreen {
				u.toggleFullscreen()
			}
		}
	}
	for {
		if _, ok := gtx.Event(pointer.Filter{Target: u, Kinds: pointer.Move | pointer.Press | pointer.Drag}); !ok {
			break
		}
		u.lastMove = gtx.Now
	}
}

func (u *UI) layout(gtx layout.Context) {
	u.update(gtx)
	paint.Fill(gtx.Ops, color.NRGBA{A: 255})
	if img := u.cap.latest.Swap(nil); img != nil {
		if u.shown != nil {
			u.cap.putBuf(u.shown) // previous frame was already uploaded by the last Frame call
		}
		u.shown = img
	}
	if u.shown != nil {
		widget.Image{Src: paint.NewImageOp(u.shown), Fit: widget.Contain, Position: layout.Center}.Layout(gtx)
	}
	// top bar auto-hides 2s after the mouse stops, unless settings are open or there's no video
	hideAt := u.lastMove.Add(2 * time.Second)
	if u.open || u.shown == nil || gtx.Now.Before(hideAt) {
		u.bar(gtx)
		gtx.Execute(op.InvalidateCmd{At: hideAt})
	}
	// topmost pass-through area so mouse movement is seen even over the bar
	pass := pointer.PassOp{}.Push(gtx.Ops)
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	event.Op(gtx.Ops, u)
	area.Pop()
	pass.Pop()
}

func (u *UI) bar(gtx layout.Context) {
	gtx.Constraints.Min = image.Point{}
	macro := op.Record(gtx.Ops)
	dims := layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(u.buttons),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !u.open {
					return layout.Dimensions{}
				}
				return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, u.panel)
			}),
		)
	})
	call := macro.Stop()
	paint.FillShape(gtx.Ops, color.NRGBA{A: 200}, clip.Rect{Max: image.Pt(gtx.Constraints.Max.X, dims.Size.Y)}.Op())
	call.Add(gtx.Ops)
}

func (u *UI) buttons(gtx layout.Context) layout.Dimensions {
	label := "Settings"
	if u.open {
		label = "Close settings"
	}
	status := *u.cap.status.Load()
	if status == "" && u.shown != nil {
		status = fmt.Sprintf("%dx%d", u.shown.Rect.Dx(), u.shown.Rect.Dy())
	}
	gap := layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout)
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(material.Button(u.th, &u.settingsBtn, label).Layout),
		gap,
		layout.Rigid(material.Button(u.th, &u.fullscreenBtn, "Fullscreen (F)").Layout),
		gap,
		layout.Rigid(material.Body2(u.th, status).Layout),
	)
}

func (u *UI) panel(gtx layout.Context) layout.Dimensions {
	gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(480))
	rows := make([]layout.FlexChild, 0, len(u.sliders)+2)
	for _, s := range u.sliders {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Dp(90)
					return material.Body2(u.th, s.name).Layout(gtx)
				}),
				layout.Flexed(1, material.Slider(u.th, s.f).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Dp(44)
					return material.Body2(u.th, fmt.Sprintf("%.2f", *s.v)).Layout(gtx)
				}),
			)
		}))
	}
	rows = append(rows,
		layout.Rigid(material.CheckBox(u.th, &u.limited, "Expand limited range (fixes washed-out greys)").Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gap := layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout)
			children := []layout.FlexChild{
				layout.Rigid(material.Button(u.th, &u.resetBtn, "Reset").Layout),
				gap,
				layout.Rigid(material.Button(u.th, &u.cameraBtn, "Change camera").Layout),
			}
			if runtime.GOOS == "windows" {
				children = append(children, gap,
					layout.Rigid(material.Button(u.th, &u.deviceBtn, "Capture card settings…").Layout))
			}
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{}.Layout(gtx, children...)
			})
		}),
	)
	if u.picking {
		rows = append(rows, layout.Rigid(u.cameraList))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

func (u *UI) cameraList(gtx layout.Context) layout.Dimensions {
	cams := u.cams.Load()
	if cams == nil || len(u.camBtns) != len(*cams) {
		return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, material.Body2(u.th, "Finding cameras…").Layout)
	}
	if len(*cams) == 0 {
		return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, material.Body2(u.th, "No cameras found").Layout)
	}
	items := make([]layout.FlexChild, len(*cams))
	for i, d := range *cams {
		label := d.Label
		if d.Value == u.cfg.Video {
			label += "  (current)"
		}
		items[i] = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return material.Button(u.th, &u.camBtns[i], label).Layout(gtx)
			})
		})
	}
	return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, items...)
	})
}
