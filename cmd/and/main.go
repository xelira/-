package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	andapp "ampersand/internal/app"
	"ampersand/internal/platform"
	"ampersand/internal/update"
)

var Version = "2.2.2-obt"

//go:embed fonts/*.ttf
var fontFS embed.FS

//go:embed icons/*.svg
var iconFS embed.FS

//go:embed icon/icon.png
var appIconPNG []byte

func appIcon() fyne.Resource {
	return fyne.NewStaticResource("and-icon.png", appIconPNG)
}

var (
	fontRegular = mustFont("fonts/PTSans-Regular.ttf")
	fontBold    = mustFont("fonts/PTSans-Bold.ttf")
	fontMono    = mustFont("fonts/JetBrainsMono-Regular.ttf")
)

func mustFont(path string) fyne.Resource {
	b, err := fontFS.ReadFile(path)
	if err != nil {
		return nil
	}
	return fyne.NewStaticResource(path, b)
}

func mustIcon(path string) fyne.Resource {
	b, err := iconFS.ReadFile("icons/" + path)
	if err != nil {
		return nil
	}
	return fyne.NewStaticResource(path, b)
}

var (
	iconDPI      = mustIcon("dpi.svg")
	iconTelegram = mustIcon("telegram.svg")
	iconWeb      = mustIcon("web.svg")
	iconPlay     = mustIcon("play.svg")
	iconStop     = mustIcon("stop.svg")
	iconCopy     = mustIcon("copy.svg")
	iconOpen     = mustIcon("open.svg")
	iconCheck    = mustIcon("check.svg")
	iconHelp     = mustIcon("help.svg")
	iconMinimize = mustIcon("minimize.svg")
	iconMaximize = mustIcon("maximize.svg")
	iconClose    = mustIcon("close.svg")
	iconGrip     = mustIcon("grip.svg")
	iconLists    = mustIcon("lists.svg")
	iconRefresh  = mustIcon("refresh.svg")
	iconPower    = mustIcon("power.svg")
)

func lightIcon(base fyne.Resource) fyne.Resource {
	if base == nil {
		return nil
	}
	return theme.NewColoredResource(base, theme.ColorNameForeground)
}

func darkIcon(base fyne.Resource) fyne.Resource {
	if base == nil {
		return nil
	}
	return theme.NewColoredResource(base, theme.ColorNameForegroundOnPrimary)
}

func dimIcon(base fyne.Resource) fyne.Resource {
	if base == nil {
		return nil
	}
	return theme.NewColoredResource(base, theme.ColorNameDisabled)
}

// Графитовая монохромная палитра: не чистый чёрный, а слои серого.
var (
	bg      = color.NRGBA{R: 16, G: 18, B: 22, A: 255}
	bgTop   = color.NRGBA{R: 24, G: 27, B: 33, A: 255}
	surface = color.NRGBA{R: 29, G: 34, B: 42, A: 255}
	raised  = color.NRGBA{R: 42, G: 48, B: 58, A: 255}
	line    = color.NRGBA{R: 58, G: 66, B: 78, A: 255}
	white   = color.NRGBA{R: 245, G: 247, B: 250, A: 255}
	gray    = color.NRGBA{R: 170, G: 180, B: 194, A: 255}
	dim     = color.NRGBA{R: 127, G: 138, B: 152, A: 255}
	ink     = color.NRGBA{R: 14, G: 16, B: 20, A: 255}
)

type monoTheme struct{}

func (monoTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return bg
	case theme.ColorNameButton:
		return raised
	case theme.ColorNameInputBackground, theme.ColorNameMenuBackground:
		return surface
	case theme.ColorNamePrimary:
		return white
	case theme.ColorNameForegroundOnPrimary:
		return ink
	case theme.ColorNameForeground:
		return white
	case theme.ColorNamePlaceHolder:
		return gray
	case theme.ColorNameDisabled:
		return dim
	case theme.ColorNameDisabledButton:
		return raised
	case theme.ColorNameSeparator:
		return line
	case theme.ColorNameHover, theme.ColorNameFocus:
		return raised
	case theme.ColorNameError:
		return white
	case theme.ColorNameForegroundOnError:
		return ink
	}
	return theme.DarkTheme().Color(name, variant)
}

func (monoTheme) Font(style fyne.TextStyle) fyne.Resource {
	if style.Monospace && fontMono != nil {
		return fontMono
	}
	if style.Bold && fontBold != nil {
		return fontBold
	}
	if fontRegular != nil {
		return fontRegular
	}
	return theme.DarkTheme().Font(style)
}
func (monoTheme) Icon(name fyne.ThemeIconName) fyne.Resource { return theme.DarkTheme().Icon(name) }
func (monoTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameHeadingText:
		return 26
	case theme.SizeNameSubHeadingText:
		return 16
	case theme.SizeNameText:
		return 14
	case theme.SizeNameCaptionText:
		return 11
	case theme.SizeNamePadding:
		return 14
	case theme.SizeNameInnerPadding:
		return 10
	case theme.SizeNameLineSpacing:
		return 6
	case theme.SizeNameInlineIcon:
		return 16
	case theme.SizeNameButtonRadius:
		return 10
	case theme.SizeNameInputRadius:
		return 10
	case theme.SizeNameSelectionRadius:
		return 8
	}
	return theme.DarkTheme().Size(name)
}

type ui struct {
	app fyne.App
	m   *andapp.Manager
	w   fyne.Window

	title, subtitle                *canvas.Text
	dpiStatus, tgStatus, webStatus *badge
	main, web                      *widget.Button
	nav                            [4]*widget.Button
	navIcons                       [4]fyne.Resource
	pages                          [4]fyne.CanvasObject
	active                         int

	profileBox   *fyne.Container
	profileBtns  map[string]*widget.Button
	profileNames map[string]string
	profileDescs map[string]string
	profileInfo  *widget.Label
	cur          string
	link         *canvas.Text
	startDPI     *widget.Button
	tgToggle     *widget.Button
	copyBtn      *widget.Button
	guidePanel   fyne.CanvasObject
	guideStep    *canvas.Text
	guideArrow   *canvas.Text
	guideBody    *widget.Label
	guideNext    *widget.Button
	guidePos     int
	mobile       bool

	listsDomains, listsIPs               *widget.Entry
	listsDomainsBuiltin, listsIPsBuiltin *widget.Label
	listsDomainsCount, listsIPsCount     *canvas.Text
	updateBtn                            *widget.Button
	updateURL                            string
	autostart                            *widget.Button
}

func main() {
	if runtime.GOOS != "android" {
		_ = os.Setenv("AND_FRAMELESS", "1")
	}
	hidden := false
	for _, arg := range os.Args[1:] {
		if arg == "--hidden" {
			hidden = true
		}
	}
	a := fyneapp.NewWithID("com.and.network")
	a.Settings().SetTheme(monoTheme{})
	a.SetIcon(appIcon())

	m, err := andapp.NewManager(Version, true)
	if err != nil {
		w := a.NewWindow("&")
		dialog.ShowError(err, w)
		w.ShowAndRun()
		return
	}
	u := &ui{app: a, m: m, profileBtns: map[string]*widget.Button{}, profileNames: map[string]string{}}
	u.w = a.NewWindow("&")
	u.w.SetIcon(appIcon())
	u.build()
	u.setupWindow()
	u.w.Show()
	u.afterShow(hidden)
	if u.firstRun() && !u.mobile {
		fyne.Do(u.startTour)
	}

	go func() {
		t := time.NewTicker(700 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			fyne.Do(u.refresh)
		}
	}()
	fyne.Do(u.refresh)
	a.Run()
}

func (u *ui) build() {
	u.mobile = runtime.GOOS == "android"
	titleSize := float32(28)
	linkSize := float32(26)
	if u.mobile {
		titleSize = 24
		linkSize = 18
	}

	// Header and common controls.
	u.title = text("DPI-ОБХОД", titleSize, white, true)
	subtitle := "NFTABLES / NFQUEUE"
	if u.mobile {
		subtitle = "VPN / BYEDPI"
	}
	u.subtitle = text(subtitle, 10, gray, false)
	u.subtitle.TextStyle.Monospace = true
	u.dpiStatus = newBadge("DPI", iconDPI)
	u.tgStatus = newBadge("TG", iconTelegram)
	u.webStatus = newBadge("WEB", iconWeb)
	if u.mobile {
		u.dpiStatus.compact = true
		u.tgStatus.compact = true
	}
	var statuses fyne.CanvasObject
	if u.mobile {
		statuses = container.NewHBox(u.dpiStatus.object(), spacer(8), u.tgStatus.object())
	} else {
		statuses = container.NewHBox(u.dpiStatus.object(), spacer(8), u.tgStatus.object(), spacer(8), u.webStatus.object())
	}
	u.updateBtn = widget.NewButtonWithIcon("", lightIcon(iconRefresh), func() { go u.updateAction() })
	u.updateBtn.Importance = widget.LowImportance

	titleBox := container.NewVBox(u.title, u.subtitle)
	var header fyne.CanvasObject
	if u.mobile {
		top := container.NewBorder(nil, nil, titleBox, u.updateBtn, nil)
		header = container.NewVBox(top, spacer(6), statuses)
	} else {
		header = container.NewBorder(nil, nil, titleBox, statuses, nil)
	}

	// DPI page.
	u.profileBox = container.NewVBox()
	profileScroll := container.NewVScroll(u.profileBox)
	if u.mobile {
		profileScroll.SetMinSize(fyne.NewSize(0, 160))
	} else {
		profileScroll.SetMinSize(fyne.NewSize(0, 190))
	}
	u.startDPI = widget.NewButtonWithIcon("ВКЛЮЧИТЬ DPI", darkIcon(iconPlay), func() { go u.toggleDPI() })
	u.startDPI.Importance = widget.HighImportance
	apply := widget.NewButtonWithIcon("ПРИМЕНИТЬ", lightIcon(iconCheck), func() { go u.applyProfile() })
	apply.Importance = widget.MediumImportance
	u.profileInfo = widget.NewLabel("")
	u.profileInfo.Wrapping = fyne.TextWrapWord
	u.profileInfo.SizeName = theme.SizeNameCaptionText
	u.profileInfo.Importance = widget.LowImportance
	u.pages[0] = card(container.NewBorder(
		container.NewVBox(label("ПРОФИЛЬ", true), separator()),
		container.NewVBox(separator(), container.NewGridWithColumns(2, u.startDPI, apply)),
		nil, nil,
		container.NewVBox(profileScroll, u.profileInfo),
	))

	// Telegram page.
	u.link = text(proxyAddress(u.m), linkSize, white, true)
	u.link.TextStyle.Monospace = true
	u.tgToggle = widget.NewButtonWithIcon("ВКЛЮЧИТЬ", darkIcon(iconTelegram), func() { go u.toggleProxy() })
	u.tgToggle.Importance = widget.HighImportance
	u.copyBtn = widget.NewButtonWithIcon("КОПИРОВАТЬ", lightIcon(iconCopy), func() {
		u.app.Clipboard().SetContent(u.m.ProxyLink())
		dialog.ShowInformation("&", "Ссылка скопирована", u.w)
	})
	openBtn := widget.NewButtonWithIcon("ОТКРЫТЬ", lightIcon(iconOpen), func() { go u.openTelegram() })
	tgActions := container.NewGridWithColumns(3, u.tgToggle, u.copyBtn, openBtn)
	if u.mobile {
		tgActions = container.NewVBox(u.tgToggle, u.copyBtn, openBtn)
	}
	u.pages[1] = card(container.NewBorder(
		container.NewVBox(label("ЛОКАЛЬНЫЙ АДРЕС", true), separator()),
		container.NewVBox(separator(), tgActions),
		nil, nil,
		container.NewCenter(u.link),
	))

	// Web / hosts page. Android build has no WEB tab at all.
	if u.mobile {
		u.web = nil
		u.pages[2] = container.NewStack()
	} else if platform.HostsSupported() {
		u.web = widget.NewButtonWithIcon("ИСПРАВИТЬ HOSTS", lightIcon(iconWeb), func() { go u.toggleWeb() })
		u.web.Importance = widget.MediumImportance
		u.pages[2] = card(container.NewBorder(
			container.NewVBox(label("АВТОМАТИЧЕСКИ", true), separator()),
			nil, nil, nil,
			container.NewVBox(
				text("Проверка hosts при запуске и откат при выходе.", 14, gray, false),
				spacer(12),
				u.web,
			),
		))
	} else {
		openWeb := widget.NewButtonWithIcon("ОТКРЫТЬ WEB.TELEGRAM.ORG", lightIcon(iconOpen), func() { u.done(u.m.OpenTelegramWeb()) })
		openWeb.Importance = widget.MediumImportance
		notice := widget.NewLabel("На Android системный файл hosts недоступен без root. Для Telegram используйте встроенный MTProto-прокси; веб-версия откроется в браузере.")
		notice.Wrapping = fyne.TextWrapWord
		notice.SizeName = theme.SizeNameCaptionText
		notice.Importance = widget.LowImportance
		u.pages[2] = card(container.NewBorder(
			container.NewVBox(label("ANDROID", true), separator()),
			nil, nil, nil,
			container.NewVBox(notice, spacer(12), openWeb),
		))
	}

	// Lists page.
	listsView := u.m.CustomLists()
	u.listsDomainsBuiltin = listLabel(strings.Join(listsView.BuiltinDomains, "\n"))
	u.listsIPsBuiltin = listLabel(strings.Join(listsView.BuiltinIPs, "\n"))
	u.listsDomains = widget.NewMultiLineEntry()
	u.listsDomains.SetPlaceHolder("example.com\nsub.example.com")
	u.listsDomains.SetMinRowsVisible(4)
	u.listsDomains.SetText(strings.Join(listsView.Domains, "\n"))
	u.listsIPs = widget.NewMultiLineEntry()
	u.listsIPs.SetPlaceHolder("1.2.3.4\n2a00:1450::/32")
	u.listsIPs.SetMinRowsVisible(4)
	u.listsIPs.SetText(strings.Join(listsView.IPs, "\n"))
	u.listsDomainsCount = text(fmt.Sprintf("%d", len(listsView.Domains)), 10, dim, true)
	u.listsDomainsCount.TextStyle.Monospace = true
	u.listsIPsCount = text(fmt.Sprintf("%d", len(listsView.IPs)), 10, dim, true)
	u.listsIPsCount.TextStyle.Monospace = true
	saveLists := widget.NewButtonWithIcon("СОХРАНИТЬ", lightIcon(iconCheck), func() { go u.saveLists() })
	saveLists.Importance = widget.HighImportance
	hint := widget.NewLabel("Домены задают SNI и Host. IP / CIDR нужны, когда трафик идёт без домена. Свои записи добавляются к встроенным.")
	hint.Wrapping = fyne.TextWrapWord
	hint.SizeName = theme.SizeNameCaptionText
	hint.Importance = widget.LowImportance
	builtinDomains := listSection("ВСТРОЕННЫЕ ДОМЕНЫ", text(fmt.Sprintf("%d", len(listsView.BuiltinDomains)), 10, dim, true))
	builtinIPs := listSection("ВСТРОЕННЫЕ IP / CIDR", text(fmt.Sprintf("%d", len(listsView.BuiltinIPs)), 10, dim, true))
	left := container.NewVBox(
		builtinDomains,
		listBox(u.listsDomainsBuiltin),
		spacer(4),
		listSection("СВОИ ДОМЕНЫ", u.listsDomainsCount),
		u.listsDomains,
	)
	right := container.NewVBox(
		builtinIPs,
		listBox(u.listsIPsBuiltin),
		spacer(4),
		listSection("СВОИ IP / CIDR", u.listsIPsCount),
		u.listsIPs,
	)
	if u.mobile {
		body := container.NewVScroll(container.NewVBox(left, spacer(12), right))
		u.pages[3] = card(container.NewBorder(
			container.NewVBox(hint, spacer(6)),
			container.NewVBox(separator(), saveLists),
			nil, nil, body))
	} else {
		u.pages[3] = card(container.NewBorder(
			container.NewVBox(hint, spacer(6)),
			container.NewVBox(separator(), container.NewBorder(nil, nil, saveLists, nil,
				label("ВСТРОЕННЫЕ: list-general-user.txt · ip-list.default", true))),
			nil, nil,
			container.NewGridWithColumns(2, left, right)))
	}

	u.buildGuide()
	u.loadProfiles()
	u.main = widget.NewButtonWithIcon("ВКЛЮЧИТЬ ВСЁ", darkIcon(iconPlay), func() { go u.toggleAll() })
	u.main.Importance = widget.HighImportance
	pageStack := container.NewStack(u.pages[0], u.pages[1], u.pages[2], u.pages[3])
	u.assemble(header, pageStack, u.main)
	u.showTab(0)
	go func() {
		time.Sleep(2 * time.Second)
		u.checkUpdates(true)
	}()
}

func listLabel(value string) *widget.Label {
	l := widget.NewLabel(value)
	l.Wrapping = fyne.TextWrapWord
	l.TextStyle = fyne.TextStyle{Monospace: true}
	l.SizeName = theme.SizeNameCaptionText
	l.Selectable = true
	return l
}

func listSection(title string, count *canvas.Text) fyne.CanvasObject {
	count.TextStyle.Monospace = true
	return container.NewBorder(nil, nil, nil, count, label(title, true))
}

func listBox(content fyne.CanvasObject) fyne.CanvasObject {
	scroll := container.NewVScroll(content)
	scroll.SetMinSize(fyne.NewSize(0, 132))
	bg := canvas.NewRectangle(raised)
	bg.CornerRadius = 10
	bg.StrokeColor = line
	bg.StrokeWidth = 1
	return container.NewStack(bg, container.NewPadded(scroll))
}

func splitLines(value string) []string {
	var out []string
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}

func (u *ui) reloadLists() {
	view := u.m.CustomLists()
	u.listsDomainsBuiltin.SetText(strings.Join(view.BuiltinDomains, "\n"))
	u.listsIPsBuiltin.SetText(strings.Join(view.BuiltinIPs, "\n"))
	u.listsDomains.SetText(strings.Join(view.Domains, "\n"))
	u.listsIPs.SetText(strings.Join(view.IPs, "\n"))
	u.listsDomainsCount.Text = fmt.Sprintf("%d", len(view.Domains))
	u.listsIPsCount.Text = fmt.Sprintf("%d", len(view.IPs))
	u.listsDomainsCount.Refresh()
	u.listsIPsCount.Refresh()
}

func (u *ui) saveLists() {
	res := u.m.SaveCustomLists(splitLines(u.listsDomains.Text), splitLines(u.listsIPs.Text))
	if res.OK {
		fyne.Do(u.reloadLists)
	}
	u.done(res)
}

func (u *ui) updateAction() {
	if u.updateURL != "" {
		if err := platform.OpenURL(u.updateURL); err != nil {
			u.fail(err.Error())
		}
		return
	}
	go u.checkUpdates(false)
}

func (u *ui) checkUpdates(silent bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	info, err := update.Check(ctx, Version, update.ReleaseAPI)
	if err != nil {
		if !silent {
			fyne.Do(func() { u.fail("Проверка обновлений: " + err.Error()) })
		}
		return
	}
	if info.Available {
		fyne.Do(func() {
			u.updateURL = info.URL
			u.updateBtn.SetText("v" + info.Latest)
			u.updateBtn.Refresh()
			if !silent {
				dialog.ShowInformation("&", "Доступна версия "+info.Latest+". Нажми кнопку с версией, чтобы открыть релиз.", u.w)
			}
		})
		return
	}
	if !silent {
		fyne.Do(func() {
			dialog.ShowInformation("&", "Обновлений нет. Установлена "+info.Current+".", u.w)
		})
	}
}

func (u *ui) firstRun() bool {
	_, err := os.Stat(filepath.Join(u.m.GetState().DataDir, "guide-v2"))
	return os.IsNotExist(err)
}

func (u *ui) buildGuide() {
	u.guideStep = text("ШАГ 1 ИЗ 6", 10, gray, false)
	u.guideStep.TextStyle.Monospace = true
	u.guideArrow = text("←", 46, white, true)
	u.guideArrow.TextStyle.Monospace = true
	u.guideBody = widget.NewLabel("")
	u.guideBody.Wrapping = fyne.TextWrapWord
	u.guideNext = widget.NewButtonWithIcon("ДАЛЕЕ", darkIcon(iconCheck), func() { u.nextGuide() })
	u.guideNext.Importance = widget.HighImportance
	skip := widget.NewButton("ПРОПУСТИТЬ", func() { u.finishGuide() })
	skip.Importance = widget.LowImportance
	row := container.NewBorder(nil, nil,
		container.NewVBox(u.guideArrow),
		container.NewVBox(u.guideNext, skip),
		u.guideBody,
	)
	u.guidePanel = card(container.NewVBox(u.guideStep, row))
	u.guidePanel.Hide()
}

func (u *ui) startTour() {
	if u.guidePanel == nil {
		return
	}
	u.guidePos = 0
	u.guidePanel.Show()
	u.renderGuide()
}

func (u *ui) finishGuide() {
	if u.guidePanel != nil {
		u.guidePanel.Hide()
	}
	_ = os.WriteFile(filepath.Join(u.m.GetState().DataDir, "guide-v2"), []byte("done\n"), 0o600)
}

func (u *ui) nextGuide() {
	u.guidePos++
	if u.guidePos >= 6 {
		u.finishGuide()
		return
	}
	u.renderGuide()
}

func (u *ui) renderGuide() {
	type step struct {
		text, arrow string
		action      func()
	}
	steps := []step{
		{"Слева выбери раздел DPI — это обход блокировок.", "←", func() { u.showTab(0) }},
		{"В списке профилей выбери GENERAL. Если не заработает — попробуй ALT.", "↓", func() { u.showTab(0); u.pickProfile("general") }},
		{"Нажми белую кнопку ВКЛЮЧИТЬ DPI. Если система попросит права — разреши.", "↓", func() { u.showTab(0) }},
		{"Для Telegram слева открой раздел TELEGRAM.", "←", func() { u.showTab(1) }},
		{"Нажми ВКЛЮЧИТЬ, затем ОТКРЫТЬ. Telegram сам предложит подключить прокси.", "↓", func() { u.showTab(1) }},
		{"Готово. Кнопка ВКЛЮЧИТЬ ВСЁ внизу включает оба модуля.", "↓", func() { u.showTab(0) }},
	}
	if u.guidePos < 0 || u.guidePos >= len(steps) {
		return
	}
	s := steps[u.guidePos]
	if s.action != nil {
		s.action()
	}
	u.guideStep.Text = fmt.Sprintf("ШАГ %d ИЗ %d", u.guidePos+1, len(steps))
	u.guideArrow.Text = s.arrow
	u.guideBody.SetText(s.text)
	if u.guidePos == len(steps)-1 {
		u.guideNext.SetText("ГОТОВО")
	} else {
		u.guideNext.SetText("ДАЛЕЕ")
	}
	u.guideStep.Refresh()
	u.guideArrow.Refresh()
}

func (u *ui) pickProfile(id string) {
	if _, ok := u.profileBtns[id]; ok {
		u.selectProfile(id)
		return
	}
	for key := range u.profileBtns {
		u.selectProfile(key)
		return
	}
}

func (u *ui) loadProfiles() {
	profiles := u.m.GetProfiles()
	st := u.m.GetState()
	u.profileBox.Objects = nil
	u.profileDescs = map[string]string{}
	compatible := make([]andapp.ProfileView, 0, len(profiles))
	for _, p := range profiles {
		if p.Compatible {
			compatible = append(compatible, p)
		}
	}
	if st.ActiveProfile != "" {
		for i, p := range compatible {
			if p.ID == st.ActiveProfile {
				compatible = append([]andapp.ProfileView{p}, append(compatible[:i], compatible[i+1:]...)...)
				break
			}
		}
	}
	for _, p := range compatible {
		id := p.ID
		name := strings.ToUpper(p.Name)
		if name == "" {
			name = id
		}
		b := widget.NewButton(name, func() { u.selectProfile(id) })
		b.Alignment = widget.ButtonAlignLeading
		b.Importance = widget.LowImportance
		u.profileBtns[id] = b
		u.profileNames[id] = name
		u.profileDescs[id] = p.Description
		u.profileBox.Add(b)
	}
	if st.ActiveProfile != "" {
		u.cur = st.ActiveProfile
	} else if len(compatible) > 0 {
		u.cur = compatible[0].ID
	}
	u.selectProfile(u.cur)
	u.profileBox.Refresh()
}

func (u *ui) selectProfile(id string) {
	if id == "" {
		return
	}
	if _, ok := u.profileBtns[id]; !ok {
		return
	}
	u.cur = id
	if u.profileInfo != nil {
		u.profileInfo.SetText(u.profileDescs[id])
	}
	for key, b := range u.profileBtns {
		name := u.profileNames[key]
		if key == id {
			b.SetText("▸ " + name)
			b.Importance = widget.HighImportance
		} else {
			b.SetText(name)
			b.Importance = widget.LowImportance
		}
		b.Refresh()
	}
}

func (u *ui) refresh() {
	st := u.m.GetState()
	u.dpiStatus.set("DPI", st.DPI.Kind == "running")
	u.tgStatus.set("TG", st.Proxy.Kind == "running")
	u.webStatus.set("WEB", st.TelegramWeb.Applied)
	if st.DPI.Kind == "running" {
		u.startDPI.SetText("ВЫКЛЮЧИТЬ DPI")
		u.startDPI.Icon = darkIcon(iconStop)
	} else {
		u.startDPI.SetText("ВКЛЮЧИТЬ DPI")
		u.startDPI.Icon = darkIcon(iconPlay)
	}
	u.startDPI.Refresh()
	if st.Proxy.Kind == "running" {
		u.tgToggle.SetText("ВЫКЛЮЧИТЬ")
		u.tgToggle.Icon = darkIcon(iconStop)
	} else {
		u.tgToggle.SetText("ВКЛЮЧИТЬ")
		u.tgToggle.Icon = darkIcon(iconTelegram)
	}
	u.tgToggle.Refresh()
	if st.Busy {
		u.main.SetText("РАБОТАЮ…")
		u.main.Icon = darkIcon(iconStop)
		u.main.Disable()
	} else {
		u.main.Enable()
		if st.DPI.Kind == "running" || st.Proxy.Kind == "running" {
			u.main.SetText("ВЫКЛЮЧИТЬ ВСЁ")
			u.main.Icon = darkIcon(iconStop)
		} else {
			u.main.SetText("ВКЛЮЧИТЬ ВСЁ")
			u.main.Icon = darkIcon(iconPlay)
		}
	}
	u.main.Refresh()
	if u.autostart != nil {
		if st.Settings.Autostart {
			u.autostart.SetText("АВТОЗАПУСК ВКЛ")
			u.autostart.Icon = lightIcon(iconCheck)
		} else {
			u.autostart.SetText("АВТОЗАПУСК")
			u.autostart.Icon = lightIcon(iconPower)
		}
		u.autostart.Refresh()
	}
	if u.web != nil {
		if st.TelegramWeb.Applied {
			u.web.SetText("ВЕРНУТЬ HOSTS")
			u.web.Icon = lightIcon(iconCheck)
		} else {
			u.web.SetText("ИСПРАВИТЬ HOSTS")
			u.web.Icon = lightIcon(iconWeb)
		}
		u.web.Refresh()
	}
}

func (u *ui) showTab(i int) {
	u.active = i
	titles := []string{"DPI-ОБХОД", "TELEGRAM-ПРОКСИ", "TELEGRAM WEB", "СПИСКИ"}
	subs := []string{"NFTABLES / NFQUEUE", "MTPROTO / WEBSOCKET", "СИСТЕМНЫЙ HOSTS", "ДОМЕНЫ И IP / CIDR"}
	if u.mobile {
		subs = []string{"VPN / BYEDPI", "MTPROTO / WEBSOCKET", "БЕЗ HOSTS", "ДОМЕНЫ И IP / CIDR"}
	}
	u.title.Text = titles[i]
	u.subtitle.Text = subs[i]
	u.title.Refresh()
	u.subtitle.Refresh()
	indices := []int{0, 1, 2, 3}
	if u.mobile {
		indices = []int{0, 1, 3}
	}
	for _, n := range indices {
		p := u.pages[n]
		if n == i {
			p.Show()
			u.nav[n].Importance = widget.HighImportance
			u.nav[n].Icon = darkIcon(u.navIcons[n])
		} else {
			p.Hide()
			u.nav[n].Importance = widget.LowImportance
			u.nav[n].Icon = lightIcon(u.navIcons[n])
		}
		u.nav[n].Refresh()
	}
}

func (u *ui) navButton(name string, base fyne.Resource, index int) *widget.Button {
	b := widget.NewButtonWithIcon(name, lightIcon(base), func() { u.showTab(index) })
	b.Alignment = widget.ButtonAlignLeading
	b.Importance = widget.LowImportance
	u.navIcons[index] = base
	return b
}

func (u *ui) toggleAll() { u.done(u.m.Toggle()) }

func (u *ui) toggleAutostart() {
	if u.autostart == nil {
		return
	}
	st := u.m.GetState()
	s := st.Settings
	s.Autostart = !s.Autostart
	u.done(u.m.SaveSettings(s))
}

func (u *ui) toggleDPI() {
	if u.m.GetState().DPI.Kind == "running" {
		u.done(u.m.StopDPI())
		return
	}
	u.done(u.m.StartDPI(u.cur))
}

func (u *ui) toggleProxy() {
	if u.m.GetState().Proxy.Kind == "running" {
		u.done(u.m.StopProxy())
		return
	}
	u.done(u.m.StartProxy())
}

func (u *ui) applyProfile() {
	if u.cur == "" {
		return
	}
	res := u.m.ApplyProfile(u.cur)
	if res.OK {
		u.done(andapp.ActionResult{OK: true, Message: res.Message})
	} else {
		u.done(andapp.ActionResult{OK: false, Message: res.Message})
	}
}

func (u *ui) toggleWeb() {
	if u.web == nil {
		return
	}
	if u.m.TelegramWebStatus().Applied {
		u.done(u.m.RevertTelegramWebFix())
		return
	}
	u.done(u.m.ApplyTelegramWebFix())
}

func (u *ui) openTelegram() {
	if u.m.GetState().Proxy.Kind != "running" {
		res := u.m.StartProxy()
		if !res.OK {
			u.done(res)
			return
		}
	}
	if err := platform.OpenURL(u.m.ProxyLink()); err != nil {
		u.fail(err.Error())
	}
}

func (u *ui) done(res andapp.ActionResult) {
	fyne.Do(func() {
		if !res.OK {
			u.fail(res.Message)
		}
		u.refresh()
	})
}

func (u *ui) fail(msg string) {
	msg = strings.TrimSpace(msg)
	if len(msg) > 700 {
		msg = msg[:700] + "…"
	}
	dialog.ShowError(errors.New(msg), u.w)
}

func proxyAddress(m *andapp.Manager) string {
	st := m.GetState()
	host := st.Settings.ProxyHost
	if host == "0.0.0.0" || host == "::" || host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("%s:%d", host, st.Settings.ProxyPort)
}

func text(s string, size float32, c color.Color, bold bool) *canvas.Text {
	t := canvas.NewText(s, c)
	t.TextSize = size
	t.TextStyle = fyne.TextStyle{Bold: bold}
	return t
}

func label(s string, mono bool) *canvas.Text {
	t := text(s, 10, gray, false)
	t.TextStyle.Monospace = mono
	return t
}

type badge struct {
	rect    *canvas.Rectangle
	label   *canvas.Text
	icon    *canvas.Image
	base    fyne.Resource
	compact bool
}

func newBadge(name string, base fyne.Resource) *badge {
	r := canvas.NewRectangle(raised)
	r.CornerRadius = 12
	r.StrokeColor = line
	r.StrokeWidth = 1
	l := text(name+" ВЫКЛ", 11, dim, true)
	l.TextStyle.Monospace = true
	img := canvas.NewImageFromResource(dimIcon(base))
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSize(16, 16))
	return &badge{rect: r, label: l, icon: img, base: base}
}

func (b *badge) object() fyne.CanvasObject {
	row := container.NewHBox(b.icon, b.label)
	return container.NewStack(b.rect, container.NewPadded(row))
}

func (b *badge) set(name string, on bool) {
	if on {
		b.rect.FillColor = white
		b.rect.StrokeColor = white
		b.label.Text = name + " ВКЛ"
		b.label.Color = ink
		b.icon.Resource = darkIcon(b.base)
	} else {
		b.rect.FillColor = raised
		b.rect.StrokeColor = line
		b.label.Text = name + " ВЫКЛ"
		b.label.Color = dim
		b.icon.Resource = dimIcon(b.base)
	}
	if b.compact {
		b.label.Text = name
	}
	b.rect.Refresh()
	b.label.Refresh()
	b.icon.Refresh()
}

func spacer(h float32) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(0, h))
	return r
}

func separator() fyne.CanvasObject {
	r := canvas.NewRectangle(line)
	r.SetMinSize(fyne.NewSize(1, 1))
	return r
}

func card(content fyne.CanvasObject) fyne.CanvasObject {
	r := canvas.NewRectangle(surface)
	r.CornerRadius = 16
	r.StrokeColor = line
	r.StrokeWidth = 1
	return container.NewStack(r, container.NewPadded(content))
}
