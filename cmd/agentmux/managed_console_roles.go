package main

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

type consoleRoleReview struct {
	id, draft, current  string
	oldVersion, version int
	err                 error
}

func (m console) reviewRoleVersion() tea.Cmd {
	review := consoleRoleReview{id: m.composeTarget, draft: string(m.text), oldVersion: m.roleVersion}
	return func() tea.Msg {
		info, err := workAgentForLabel(context.Background(), m.client, review.id)
		review.err = err
		if err == nil {
			review.current, review.version = info.Config.Role, info.RoleVersion
		}
		return review
	}
}

func (m *console) receiveRoleReview(review consoleRoleReview) {
	m.busy = false
	if m.composer != "role" || m.composeTarget != review.id || string(m.text) != review.draft || m.roleVersion != review.oldVersion {
		m.message = "Role review discarded because the draft changed."
		return
	}
	if review.err != nil {
		m.message = "Role review unavailable; draft kept: " + activityText(review.err.Error(), 150)
		return
	}
	m.reader = &consoleReader{title: "Review role · " + review.id, body: consoleContent(fmt.Sprintf("Latest saved role · version %d\n%s\n\nYour retained draft · original version %d\n%s\n\nEnter adopts only this reviewed version for your draft. No role is saved until you submit from the editor; a newer concurrent edit still conflicts.", review.version, review.current, review.oldVersion, review.draft), 1<<20), roleReview: &review}
}
