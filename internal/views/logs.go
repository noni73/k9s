package views

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/derailed/k9s/internal/resource"
	"github.com/derailed/tview"
	"github.com/gdamore/tcell"
	"github.com/rs/zerolog/log"
)

const (
	maxBuff1    int64 = 200
	refreshRate       = 200 * time.Millisecond
	maxCleanse        = 100
)

type logsView struct {
	*tview.Pages

	parentView string
	parent     loggable
	containers []string
	actions    keyActions
	cancelFunc context.CancelFunc
	autoScroll bool
}

func newLogsView(pview string, parent loggable) *logsView {
	v := logsView{
		Pages:      tview.NewPages(),
		parent:     parent,
		parentView: pview,
		autoScroll: true,
		containers: []string{},
	}
	v.setActions(keyActions{
		tcell.KeyEscape: {description: "Back", action: v.backCmd, visible: true},
		KeyC:            {description: "Clear", action: v.clearCmd, visible: true},
		KeyS:            {description: "Toggle AutoScroll", action: v.toggleScrollCmd, visible: true},
		KeyG:            {description: "Top", action: v.topCmd, visible: false},
		KeyShiftG:       {description: "Bottom", action: v.bottomCmd, visible: false},
		KeyF:            {description: "Up", action: v.pageUpCmd, visible: false},
		KeyB:            {description: "Down", action: v.pageDownCmd, visible: false},
	})
	v.SetInputCapture(v.keyboard)

	return &v
}

// Protocol...

func (v *logsView) init() {
	v.load(0)
}

func (v *logsView) keyboard(evt *tcell.EventKey) *tcell.EventKey {
	key := evt.Key()
	if key == tcell.KeyRune {
		key = tcell.Key(evt.Rune())
	}

	if kv, ok := v.CurrentPage().Item.(keyHandler); ok {
		if kv.keyboard(evt) == nil {
			return nil
		}
	}

	if evt.Key() == tcell.KeyRune {
		if i, err := strconv.Atoi(string(evt.Rune())); err == nil {
			if _, ok := numKeys[i]; ok {
				v.load(i - 1)
				return nil
			}
		}
	}

	if m, ok := v.actions[key]; ok {
		log.Debug().Msgf(">> LogsView handled %s", tcell.KeyNames[key])
		return m.action(evt)
	}

	return evt
}

// SetActions to handle keyboard events.
func (v *logsView) setActions(aa keyActions) {
	v.actions = aa
}

// Hints show action hints
func (v *logsView) hints() hints {
	if len(v.containers) > 1 {
		for i, c := range v.containers {
			v.actions[tcell.Key(numKeys[i+1])] = newKeyAction(c, nil, true)
		}
	}

	return v.actions.toHints()
}

func (v *logsView) addContainer(n string) {
	v.containers = append(v.containers, n)
	l := newLogView(n, v.parent)
	{
		l.SetInputCapture(v.keyboard)
		l.backFn = v.backCmd
	}
	v.AddPage(n, l, true, false)
}

func (v *logsView) deleteAllPages() {
	for i, c := range v.containers {
		v.RemovePage(c)
		delete(v.actions, tcell.Key(numKeys[i+1]))
	}
	v.containers = []string{}
}

func (v *logsView) stop() {
	if v.cancelFunc == nil {
		return
	}
	log.Debug().Msg("Canceling logs...")
	v.cancelFunc()
	v.cancelFunc = nil
}

func (v *logsView) load(i int) {
	if i < 0 || i > len(v.containers)-1 {
		return
	}
	v.SwitchToPage(v.containers[i])
	if err := v.doLoad(v.parent.getSelection(), v.containers[i]); err != nil {
		v.parent.appView().flash(flashErr, err.Error())
		l := v.CurrentPage().Item.(*logView)
		l.logLine("😂 Doh! No logs are available at this time. Check again later on...", false)
		return
	}
	v.parent.appView().SetFocus(v)
}

func (v *logsView) doLoad(path, co string) error {
	v.stop()

	c := make(chan string)
	go func() {
		l := v.CurrentPage().Item.(*logView)
		l.Clear()
		l.setTitle(path + ":" + co)
		for {
			select {
			case line, ok := <-c:
				if !ok {
					if v.autoScroll {
						l.ScrollToEnd()
					}
					return
				}
				l.logLine(line, v.autoScroll)
			}
		}
	}()

	ns, po := namespaced(path)
	res, ok := v.parent.getList().Resource().(resource.Tailable)
	if !ok {
		return fmt.Errorf("Resource %T is not tailable", v.parent.getList().Resource)
	}
	maxBuff := int64(v.parent.appView().config.K9s.LogRequestSize)
	cancelFn, err := res.Logs(c, ns, po, co, maxBuff, false)
	if err != nil {
		cancelFn()
		return err
	}
	v.cancelFunc = cancelFn

	return nil
}

// ----------------------------------------------------------------------------
// Actions...

func (v *logsView) toggleScrollCmd(evt *tcell.EventKey) *tcell.EventKey {
	v.autoScroll = !v.autoScroll
	if v.autoScroll {
		v.parent.appView().flash(flashInfo, "Autoscroll is on")
	} else {
		v.parent.appView().flash(flashInfo, "Autoscroll is off")
	}

	return nil
}

func (v *logsView) backCmd(evt *tcell.EventKey) *tcell.EventKey {
	v.stop()
	v.parent.switchPage(v.parentView)

	return nil
}

func (v *logsView) topCmd(evt *tcell.EventKey) *tcell.EventKey {
	if p := v.CurrentPage(); p != nil {
		v.parent.appView().flash(flashInfo, "Top of logs...")
		p.Item.(*logView).ScrollToBeginning()
	}

	return nil
}

func (v *logsView) bottomCmd(*tcell.EventKey) *tcell.EventKey {
	if p := v.CurrentPage(); p != nil {
		v.parent.appView().flash(flashInfo, "Bottom of logs...")
		p.Item.(*logView).ScrollToEnd()
	}

	return nil
}

func (v *logsView) pageUpCmd(*tcell.EventKey) *tcell.EventKey {
	if p := v.CurrentPage(); p != nil {
		if p.Item.(*logView).PageUp() {
			v.parent.appView().flash(flashInfo, "Reached Top ...")
		}
	}

	return nil
}

func (v *logsView) pageDownCmd(*tcell.EventKey) *tcell.EventKey {
	if p := v.CurrentPage(); p != nil {
		if p.Item.(*logView).PageDown() {
			v.parent.appView().flash(flashInfo, "Reached Bottom ...")
		}
	}

	return nil
}

func (v *logsView) clearCmd(*tcell.EventKey) *tcell.EventKey {
	if p := v.CurrentPage(); p != nil {
		v.parent.appView().flash(flashInfo, "Clearing logs...")
		p.Item.(*logView).Clear()
	}

	return nil
}
