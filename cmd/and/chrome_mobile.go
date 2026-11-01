//go:build android

package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

func (u *ui) assemble(header, pageStack, mainButton fyne.CanvasObject) {
	u.nav[0] = u.navButton("DPI", iconDPI, 0)
	u.nav[1] = u.navButton("TELEGRAM", iconTelegram, 1)
	u.nav[3] = u.navButton("СПИСКИ", iconLists, 3)
	u.nav[1].SetText("TG")
	u.nav[3].SetText("ЛИСТЫ")
	for _, b := range []*widget.Button{u.nav[0], u.nav[1], u.nav[3]} {
		b.Alignment = widget.ButtonAlignCenter
	}
	bottomNav := container.NewGridWithColumns(3, u.nav[0], u.nav[1], u.nav[3])
	bottom := container.NewVBox(mainButton, separator(), bottomNav)
	body := container.NewBorder(header, bottom, nil, nil, pageStack)
	rootBg := canvas.NewLinearGradient(bgTop, bg, 90)
	u.w.SetContent(container.NewStack(rootBg, container.NewPadded(body)))
}

func (u *ui) setupWindow() {}

func (u *ui) afterShow(bool) {}
