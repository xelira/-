//go:build !android

package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

type windowControls interface {
	MinimizeWindow()
	MaximizeWindow()
	MoveWindow(int, int)
	StartWindowDrag()
	DragWindowToCursor()
	EndWindowDrag()
}

type dragBar struct {
	widget.BaseWidget
	onStart func()
	onMove  func()
	onEnd   func()
	started bool
	accum   float32
}

func (d *dragBar) CreateRenderer() fyne.WidgetRenderer {
	mark := text("&", 22, white, true)
	name := text("AMPERSAND", 11, white, true)
	ver := text("2.2.2 ОБТ", 9, gray, false)
	ver.TextStyle.Monospace = true
	return widget.NewSimpleRenderer(container.NewHBox(mark, spacer(8), name, spacer(6), ver))
}

func (d *dragBar) MinSize() fyne.Size { return fyne.NewSize(240, 40) }

func (d *dragBar) Dragged(e *fyne.DragEvent) {
	if !d.started {
		d.accum += float32(abs(e.Dragged.DX)) + float32(abs(e.Dragged.DY))
		if d.accum < 4 {
			return
		}
		d.started = true
		if d.onStart != nil {
			d.onStart()
		}
	}
	if d.onMove != nil {
		d.onMove()
	}
}
func (d *dragBar) DragEnd() {
	d.started = false
	d.accum = 0
	if d.onEnd != nil {
		d.onEnd()
	}
}

func abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

type resizeGrip struct {
	widget.BaseWidget
	onDrag func(float32, float32)
}

func (g *resizeGrip) CreateRenderer() fyne.WidgetRenderer {
	img := canvas.NewImageFromResource(lightIcon(iconGrip))
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSize(18, 18))
	return widget.NewSimpleRenderer(img)
}

func (g *resizeGrip) MinSize() fyne.Size { return fyne.NewSize(22, 22) }
func (g *resizeGrip) Dragged(e *fyne.DragEvent) {
	if g.onDrag != nil {
		g.onDrag(e.Dragged.DX, e.Dragged.DY)
	}
}
func (g *resizeGrip) DragEnd() {}

func (u *ui) controls() windowControls {
	c, _ := u.w.(windowControls)
	return c
}

func (u *ui) startWindowDrag() {
	if c := u.controls(); c != nil {
		c.StartWindowDrag()
	}
}

func (u *ui) dragWindow() {
	if c := u.controls(); c != nil {
		c.DragWindowToCursor()
	}
}

func (u *ui) endWindowDrag() {
	if c := u.controls(); c != nil {
		c.EndWindowDrag()
	}
}

func (u *ui) resizeWindow(dx, dy float32) {
	size := u.w.Canvas().Size()
	u.w.Resize(fyne.NewSize(size.Width+dx, size.Height+dy))
}

func (u *ui) titleBar() fyne.CanvasObject {
	drag := &dragBar{onStart: u.startWindowDrag, onMove: u.dragWindow, onEnd: u.endWindowDrag}
	min := widget.NewButtonWithIcon("", lightIcon(iconMinimize), func() {
		if c := u.controls(); c != nil {
			c.MinimizeWindow()
		}
	})
	max := widget.NewButtonWithIcon("", lightIcon(iconMaximize), func() {
		if c := u.controls(); c != nil {
			c.MaximizeWindow()
		}
	})
	closeBtn := widget.NewButtonWithIcon("", lightIcon(iconClose), func() { u.w.Close() })
	for _, b := range []*widget.Button{min, max, closeBtn} {
		b.Importance = widget.LowImportance
	}
	controls := container.NewHBox(min, max, closeBtn)
	row := container.NewBorder(nil, nil, nil, controls, drag)
	bg := canvas.NewRectangle(surface)
	bg.StrokeColor = line
	bg.StrokeWidth = 1
	return container.NewStack(bg, container.NewPadded(row))
}

func (u *ui) assemble(header, pageStack, mainButton fyne.CanvasObject) {
	u.nav[0] = u.navButton("DPI", iconDPI, 0)
	u.nav[1] = u.navButton("TELEGRAM", iconTelegram, 1)
	u.nav[2] = u.navButton("WEB", iconWeb, 2)
	u.nav[3] = u.navButton("СПИСКИ", iconLists, 3)
	nav := container.NewVBox(u.nav[0], u.nav[1], u.nav[2], u.nav[3])

	root := text("ROOT", 10, gray, false)
	root.TextStyle.Monospace = true
	help := widget.NewButtonWithIcon("", lightIcon(iconHelp), func() { u.startTour() })
	help.Importance = widget.LowImportance
	u.autostart = widget.NewButtonWithIcon("АВТОЗАПУСК", lightIcon(iconPower), func() { go u.toggleAutostart() })
	u.autostart.Importance = widget.LowImportance

	side := container.NewVBox(spacer(14), nav, layout.NewSpacer(), u.autostart, container.NewHBox(root, help, u.updateBtn))
	sideBg := canvas.NewLinearGradient(surface, bg, 90)
	sidePanel := container.NewStack(sideBg, container.NewPadded(side))

	center := container.NewBorder(u.guidePanel, nil, nil, nil, pageStack)
	grip := &resizeGrip{onDrag: u.resizeWindow}
	bottom := container.NewBorder(nil, nil, nil, grip, mainButton)
	main := container.NewBorder(header, container.NewVBox(separator(), bottom), nil, nil, center)

	split := container.NewHSplit(sidePanel, container.NewPadded(main))
	split.SetOffset(0.22)
	rootBg := canvas.NewLinearGradient(bgTop, bg, 90)
	bar := u.titleBar()
	u.w.SetContent(container.NewBorder(bar, nil, nil, nil, container.NewStack(rootBg, split)))
}

func (u *ui) setupWindow() {
	u.w.Resize(fyne.NewSize(960, 680))
	u.w.SetFixedSize(false)
	u.w.SetPadded(false)
	u.w.CenterOnScreen()
	u.w.SetCloseIntercept(func() {
		u.m.Close()
		u.app.Quit()
	})
}

func (u *ui) afterShow(hidden bool) {
	if !hidden {
		return
	}
	if c := u.controls(); c != nil {
		c.MinimizeWindow()
	}
}
