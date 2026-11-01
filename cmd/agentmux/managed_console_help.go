package main

import "strings"

// Help is a frozen reader overlay. It never changes a draft, recipient,
// permission selection or task state, and polling cannot retarget it.
func (m *console) openContextHelp() {
	title, body := "Workbench help", ""
	if m.teamAction != nil {
		title = "Review help"
		body = "This review freezes the selected identities and current version. Confirm only the displayed action. Esc returns without submitting. A conflict requires a fresh review; it never silently retargets an action. Invitations require the invited agent's consent. Removing an Agentmux-owned session can stop its descendant processes; disconnecting an external bridge leaves its original terminal running."
	} else if m.composer != "" {
		title = "Editor help · " + m.composer
		body = "Enter submits the displayed draft. Alt-Enter inserts a newline. Arrow keys move the cursor; Home/End move within a line, Ctrl-A/Ctrl-E to the whole draft's start/end. Ctrl-U clears. Esc cancels. Ctrl-G opens this help without changing the draft. Printable characters, including ?, remain literal text."
		switch m.composer {
		case "relaunch":
			body += "\n\nThe stable ID above is frozen. Enter with an empty selection deliberately starts a fresh native conversation. Type last to continue the provider's latest project conversation, picker to choose in its native UI, or an exact provider conversation ID. No history is guessed. The native CLI must have exited; healthy peer processes and the workspace attempt are preserved. After an uncertain response inspect sessions before trying again."
		case "send", "reply", "message":
			body += "\n\nThe intent above the editor is frozen for this draft. Ctrl-T switches between an informational note and an instruction task. Notes go to the broker inbox; instruction tasks can be claimed by managed workers. Interactive native agents must read their broker inbox/tasks explicitly. Neither choice injects keystrokes or starts a native model turn.\n\nReplies retain broker-validated ancestry. Every direct recipient must be able to see the referenced item; replies cannot widen the audience. A failed draft restores its original recipient, reference and intent with Ctrl-R from the workbench. Inspect possible delivery before retrying after transport errors."
		case "board":
			body += "\n\nThis is a shared informational note. The exact board namespace appears above the editor; a reply retains that board and structured ancestry. It never creates an instruction task. Different boards or private follow-ups require an explicit destination."
		case "memory-value":
			body += "\n\nMemory saves use the displayed namespace, key and CAS version. Ctrl-R reviews the latest saved value; Enter in that review adopts only its version, without saving. Review your retained draft before submitting. Private memory belongs to the selected authenticated agent. Imported source metadata is a claim, not authenticated identity."
		case "role":
			body += "\n\nRole changes use the exact session and role version captured when opening this editor. Native sessions receive an informational inbox notice and can query identity for the current role; no terminal input is injected. Managed workers capture the current role when claiming their next task. In-flight turns keep their existing input. If another editor changes the role, refresh and review before submitting your retained draft again."
		case "find":
			body += "\n\nSearch task title, project, provider, stable ID or terminal location. Up/Down chooses a match; Enter selects it without attaching or sending input."
		default:
			if strings.HasPrefix(m.composer, "new-") {
				body += "\n\nA title is a human label. Choose coordinated to append Agentmux broker context and an optional role, or native-only to inject no prompt. Native Antigravity currently requires native-only; use the Pi bridge for injected coordination context. The provider's normal interactive tools and approvals remain available in local mode. A stable ID is generated independently of its title."
			}
		}
	} else {
		views := []struct{ title, body string }{
			{"Board", "[ / ] select a message, o reads its full text, r replies. A board reply retains its board; a direct reply retains the visible conversation. b posts to the selected board; +/- changes the board. s composes to the selected session. The editor shows note versus instruction intent before submission."},
			{"Agents", "Up/Down selects a stable session ID. Enter opens the original native CLI for full interaction; F1 returns to this workbench. / searches and L renames the human task title. N creates a session. B edits four admission reservation fields with a final CAS review (provider billing is not capped). x reviews removal; an external bridge is disconnected without closing its native terminal."},
			{"Connections", "Up/Down selects the sender; Left/Right selects its recipient. m, p and i review note, ping and instruction permissions. c creates a scoped pair; d makes the sender independent across groups. Groups add independent contributions; explicit owner denies win. M/P/I remove an exact override. o shows effective-versus-stored sources. Existing accepted tasks are not cancelled by permission changes: review Tasks separately."},
			{"Memory", "Choose the displayed namespace before reading or editing. Effective read/write can differ from stored owner intent due to isolation, membership or ancestry. R resets an override without granting membership. n creates a key; v refreshes entries; e edits the selected value with its current CAS version. o reads full content and source claims. A conflict preserves the draft; Ctrl-R in the editor reviews the latest version without saving."},
			{"Tasks", "Select a task to inspect its delivery state, output and trace. PgDn loads older tasks; PgUp returns toward newer tasks. f searches the complete task history by text, result or ID; an empty search clears the filter. o reads full owner-visible content; r creates a structured reply. Cancellation is explicit and can remain uncertain after transport loss. Inspect side effects before resolving or retrying; a new attempt is charged again."},
			{"Existing terminals", "Discovery is read-only: it inspects current-user process/terminal identities without reading prompts or credentials. c prepares a bridge. Paste its setup into the original native conversation yourself; Agentmux never injects setup or user turns. Observed IDs and registered session IDs are different."},
			{"Teams", "Owner collaboration groups provide scoped boards/memory. Consented dynamic teams use invitations accepted by each agent. Select the exact team and inspect its members, ownership, namespaces and limits before a live change. e edits name/description; t proposes the selected member as owner. Ownership changes only after the proposed agent accepts using its own credential; team move likewise consumes a participant invitation with consent and both CAS versions. Lifetime allocations are not refunded when a team is dissolved."},
			{"Events", "Inspect lifecycle and policy events alongside the current task state. Polling does not imply a model is active. Research controls pause/resume/step/stop the study runner, while native terminal interaction remains separate. An offline observer never grants authority to mutate state."},
		}
		index := m.tab
		if index < 0 || index >= len(views) {
			index = 0
		}
		title, body = views[index].title+" help", views[index].body
		body += "\n\nTab/Shift-Tab switches views; 1–8 selects one directly. W opens the saved workspace chooser; Enter opens a separate observer and q returns to the previous one. U reviews an individual exited native session relaunch. V edits its role. ? opens context help. q detaches/closes the observer while sessions keep running. Titles may repeat; operations use the stable IDs shown with them. Every workspace action targets its selected state directory."
	}
	m.reader = &consoleReader{title: title, body: body}
}
