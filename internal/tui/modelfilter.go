package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// modelFilterRowMarker prefixes the highlighted row so focusedOverlayLine
// can find it, same as settingsRowMarker.
const modelFilterRowMarker = "▸ "

// modelFilterRow is one model on the hidden-models screen, whose toggle is on for a shown model.
type modelFilterRow struct {
	agent, id, label string
}

// modelFilterRows lists every named model the core serves, agent by agent,
// in the agent's own order. Agents with only "default" have nothing to hide.
func (m *Model) modelFilterRows() []modelFilterRow {
	var rows []modelFilterRow
	for _, o := range m.agentOptions {
		for _, id := range o.Models {
			if id == "default" {
				continue
			}
			rows = append(rows, modelFilterRow{o.Name, id, m.modelLabels(o.Name, []string{id})[0]})
		}
	}
	return rows
}

// hiddenModelCount is the settings row's value: how many models are hidden.
func (m *Model) hiddenModelCount() int {
	n := 0
	for _, r := range m.modelFilterRows() {
		if m.cfg.ModelExcluded(r.agent, r.id) {
			n++
		}
	}
	return n
}

// openModelFilter opens ModeModelFilter from the settings screen, which Esc
// returns to.
func (m *Model) openModelFilter() {
	m.modelFilterCursor = 0
	m.mode = ModeModelFilter
	m.resetOverlayViewport()
}

func (m *Model) renderModelFilter() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("MODELS SHOWN IN PICKERS"))
	b.WriteString("\n\n")
	rows := m.modelFilterRows()
	if len(rows) == 0 {
		b.WriteString(muteStyle.Render("No model lists yet. They come from agent-usage."))
		b.WriteString("\n")
		return b.String()
	}
	// listRow/listRowSelected each add one column of padding on both sides.
	rowWidth := m.overlayWidth(formHintWidth) - 2
	for i, r := range rows {
		if i == 0 || rows[i-1].agent != r.agent {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(muteStyle.Render(r.agent))
			b.WriteString("\n")
		}
		prefix := "  "
		if i == m.modelFilterCursor {
			prefix = modelFilterRowMarker
		}
		value := renderToggle(!m.cfg.ModelExcluded(r.agent, r.id), false)
		labelWidth := max(rowWidth-lipgloss.Width(prefix)-lipgloss.Width(value)-1, 4)
		line := prefix + fmt.Sprintf("%-*s", labelWidth, truncate(r.label, labelWidth)) + " " + value
		if i == m.modelFilterCursor {
			line = listRowSelected.Render(line)
		} else {
			line = listRow.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// modelFilterFooter mirrors settingsFooter's width-tiered approach.
func (m *Model) modelFilterFooter() string {
	controls := "↑↓ select  enter/←→ show or hide  esc back"
	if lipgloss.Width(controls) > m.overlayWidth(formHintWidth) {
		controls = "esc back  enter toggle"
	}
	return muteStyle.Render(controls)
}

// updateModelFilter handles keys while ModeModelFilter is open. Enter, space
// or ←→ flips the highlighted model between shown and hidden and saves it
// at once, like the settings screen's toggles.
func (m *Model) updateModelFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.modelFilterRows()
	switch {
	case key.Matches(msg, m.keys.Cancel):
		m.mode = ModeSettings
		return m, nil
	case len(rows) == 0:
		return m, nil
	case key.Matches(msg, m.keys.Up):
		m.modelFilterCursor = (m.modelFilterCursor - 1 + len(rows)) % len(rows)
	case key.Matches(msg, m.keys.Down):
		m.modelFilterCursor = (m.modelFilterCursor + 1) % len(rows)
	case key.Matches(msg, m.keys.Enter), key.Matches(msg, m.keys.Left), key.Matches(msg, m.keys.Right), msg.String() == " ":
		return m.toggleModelExcluded(rows[m.modelFilterCursor])
	}
	return m, nil
}

// toggleModelExcluded saves r's new state, writing m.cfg directly the way
// applySettingsRow does, and for the same reasons: the save is synchronous,
// and cfgAppliedAt keeps an older streamed snapshot from undoing it.
func (m *Model) toggleModelExcluded(r modelFilterRow) (tea.Model, tea.Cmd) {
	excluded := !m.cfg.ModelExcluded(r.agent, r.id)
	if err := m.backend.SetModelExcluded(r.agent, r.id, excluded); err != nil {
		return m.flashError(fmt.Errorf("hide model: %w", err))
	}
	m.cfg.SetModelExcluded(r.agent, r.id, excluded)
	m.cfgAppliedAt = time.Now()
	if excluded {
		m.setFlash("info", r.label+": hidden from model pickers")
	} else {
		m.setFlash("info", r.label+": shown")
	}
	return m, nil
}
