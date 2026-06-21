package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
)

// Settings is the user-editable runtime configuration shown in the Настройки
// section. main.go seeds it from the parsed CLI; ApplySettings reloads the core.
type Settings struct {
	SocksPort        int
	ClashEnabled     bool
	ClashAddr        string
	URLTestURL       string
	URLTestInterval  string
	URLTestTolerance int
	SaveProfile      bool
}

type sfKind int

const (
	sfInt sfKind = iota
	sfText
	sfToggle
	sfAction
)

type sfField struct {
	label  string
	kind   sfKind
	action string // for sfAction: "apply" | "daemon"
}

// settingsFields is the static field list (order = display + focus order).
var settingsFields = []sfField{
	{"SOCKS-порт", sfInt, ""},
	{"Clash API", sfToggle, ""},
	{"Clash адрес", sfText, ""},
	{"urltest URL", sfText, ""},
	{"urltest интервал", sfText, ""},
	{"urltest допуск, мс", sfInt, ""},
	{"Сохранять ключ", sfToggle, ""},
	{"Применить", sfAction, "apply"},
	{"Запустить в фоне (daemon)", sfAction, "daemon"},
}

// settingsFieldsFor returns the field list, swapping the last action to
// "Остановить демон" when attached to a remote instance (vs "Запустить в фоне"
// for a local run).
func (m Model) settingsFieldsFor() []sfField {
	f := make([]sfField, len(settingsFields))
	copy(f, settingsFields)
	if m.attached {
		f[len(f)-1] = sfField{"Остановить демон", sfAction, "stopdaemon"}
	}
	return f
}

// settingsForm is the editing state for the Настройки section.
type settingsForm struct {
	focus   int
	editing bool
	edit    textinput.Model
	draft   Settings
}

// display renders a field's current value for the (non-editing) view.
func (s Settings) display(i int) string {
	switch i {
	case 0:
		return strconv.Itoa(s.SocksPort)
	case 1:
		return onOff(s.ClashEnabled)
	case 2:
		return orDash(s.ClashAddr)
	case 3:
		return orDash(s.URLTestURL)
	case 4:
		return orDash(s.URLTestInterval)
	case 5:
		return strconv.Itoa(s.URLTestTolerance)
	case 6:
		return yesNo(s.SaveProfile)
	}
	return ""
}

// editable is the raw value seeded into the inline editor for text/int fields.
func (s Settings) editable(i int) string {
	switch i {
	case 0:
		if s.SocksPort == 0 {
			return ""
		}
		return strconv.Itoa(s.SocksPort)
	case 2:
		return s.ClashAddr
	case 3:
		return s.URLTestURL
	case 4:
		return s.URLTestInterval
	case 5:
		if s.URLTestTolerance == 0 {
			return ""
		}
		return strconv.Itoa(s.URLTestTolerance)
	}
	return ""
}

// setField commits an edited text/int field back into the draft.
func (s *Settings) setField(i int, v string) {
	v = strings.TrimSpace(v)
	switch i {
	case 0:
		s.SocksPort, _ = strconv.Atoi(v)
	case 2:
		s.ClashAddr = v
	case 3:
		s.URLTestURL = v
	case 4:
		s.URLTestInterval = v
	case 5:
		s.URLTestTolerance, _ = strconv.Atoi(v)
	}
}

// toggle flips a boolean field.
func (s *Settings) toggle(i int) {
	switch i {
	case 1:
		s.ClashEnabled = !s.ClashEnabled
	case 6:
		s.SaveProfile = !s.SaveProfile
	}
}

func onOff(b bool) string {
	if b {
		return "вкл"
	}
	return "выкл"
}

func yesNo(b bool) string {
	if b {
		return "да"
	}
	return "нет"
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
