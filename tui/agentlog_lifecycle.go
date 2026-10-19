// ABOUTME: AgentLog's node-lifecycle message handling (completed, failed, retrying,
// ABOUTME: warning and info lines), split out of agentlog.go for the file-size ratchet.
package tui

import tea "github.com/charmbracelet/bubbletea"

// applyStreamMsgLifecycle handles node lifecycle messages (completed, failed, retrying).
func (al *AgentLog) applyStreamMsgLifecycle(msg tea.Msg) bool {
	switch m := msg.(type) {
	case MsgNodeFailed:
		al.flushNode(m.NodeID)
		al.addTaggedLine(m.NodeID, Styles.Error.Render("FAILED: "+m.Error), LineError)
		delete(al.streams, m.NodeID)
	case MsgNodeRetrying:
		al.flushNode(m.NodeID)
		al.addTaggedLine(m.NodeID, Styles.Warn.Render("RETRYING: "+m.Message), LineError)
	case MsgNodeWarning:
		al.flushNode(m.NodeID)
		al.addTaggedLine(m.NodeID, Styles.Warn.Render("⚠ WARNING: "+m.Message), LineError)
	case MsgNodeInfo:
		al.flushNode(m.NodeID)
		al.addTaggedLine(m.NodeID, Styles.Muted.Render(m.Message), LineGeneral)
	case MsgNodeCompleted:
		al.flushNode(m.NodeID)
		delete(al.streams, m.NodeID)
	default:
		return false
	}
	return true
}
