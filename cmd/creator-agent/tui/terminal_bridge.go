package tui

import (
	term "github.com/skys-mission/creator-agent/cmd/creator-agent/tui/terminal"
)

type (
	Terminal    = term.Terminal
	Event       = term.Event
	EventKey    = term.EventKey
	EventResize = term.EventResize
	EventMouse  = term.EventMouse
	Key         = term.Key
	ModMask     = term.ModMask
)

const (
	KeyRune       = term.KeyRune
	KeyEnter      = term.KeyEnter
	KeyTab        = term.KeyTab
	KeyBacktab    = term.KeyBacktab
	KeyBackspace  = term.KeyBackspace
	KeyBackspace2 = term.KeyBackspace2
	KeyDelete     = term.KeyDelete
	KeyLeft       = term.KeyLeft
	KeyRight      = term.KeyRight
	KeyUp         = term.KeyUp
	KeyDown       = term.KeyDown
	KeyHome       = term.KeyHome
	KeyEnd        = term.KeyEnd
	KeyPgUp       = term.KeyPgUp
	KeyPgDn       = term.KeyPgDn
	KeyEsc        = term.KeyEsc
	KeyCtrlA      = term.KeyCtrlA
	KeyCtrlB      = term.KeyCtrlB
	KeyCtrlC      = term.KeyCtrlC
	KeyCtrlD      = term.KeyCtrlD
	KeyCtrlE      = term.KeyCtrlE
	KeyCtrlF      = term.KeyCtrlF
	KeyCtrlG      = term.KeyCtrlG
	KeyCtrlH      = term.KeyCtrlH
	KeyCtrlI      = term.KeyCtrlI
	KeyCtrlJ      = term.KeyCtrlJ
	KeyCtrlK      = term.KeyCtrlK
	KeyCtrlL      = term.KeyCtrlL
	KeyCtrlM      = term.KeyCtrlM
	KeyCtrlN      = term.KeyCtrlN
	KeyCtrlO      = term.KeyCtrlO
	KeyCtrlP      = term.KeyCtrlP
	KeyCtrlQ      = term.KeyCtrlQ
	KeyCtrlR      = term.KeyCtrlR
	KeyCtrlS      = term.KeyCtrlS
	KeyCtrlT      = term.KeyCtrlT
	KeyCtrlU      = term.KeyCtrlU
	KeyCtrlV      = term.KeyCtrlV
	KeyCtrlW      = term.KeyCtrlW
	KeyCtrlX      = term.KeyCtrlX
	KeyCtrlY      = term.KeyCtrlY
	KeyCtrlZ      = term.KeyCtrlZ
	ModNone       = term.ModNone
	ModAlt        = term.ModAlt
	ModCtrl       = term.ModCtrl

	MouseLeft      = term.MouseLeft
	MouseMiddle    = term.MouseMiddle
	MouseRight     = term.MouseRight
	MouseWheelUp   = term.MouseWheelUp
	MouseWheelDown = term.MouseWheelDown
)

var (
	NewEventKey          = term.NewEventKey
	OpenTerminal         = term.Open
	PumpInput            = term.PumpInput
	Decode               = term.Decode
	RestoreTerminal      = term.Restore
	WriteOSC52           = term.WriteOSC52
	RedirectStderrToFile = term.RedirectStderrToFile
)

func stdinReader() termReader { return term.StdinReader() }

type termReader interface {
	Read(p []byte) (n int, err error)
}
