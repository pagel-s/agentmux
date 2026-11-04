package main

import (
	"context"
	"errors"
	"fmt"

	"agentmux/internal/managed"
	tea "github.com/charmbracelet/bubbletea"
)

type consoleTaskPairRead struct {
	state, from, to string
	request         uint64
	review          workPairReview
	err             error
}

type consoleTaskPairApplied struct {
	state  string
	review consoleOwnerReview
	err    error
}

func taskPairOwnerReview(pair workPairReview) *consoleOwnerReview {
	return &consoleOwnerReview{
		operation: "control.pair", title: "Review task pairing",
		body: "1 Review each item · 2 Auto · Enter applies · Esc cancels\n\n" + pair.body,
		args: pair.args(), pair: &pair, requestID: requestIDForWork(),
		message: "Task pair saved. Inspect Tasks and native delivery for actual outcomes.",
	}
}

func (m *console) reviewTaskPair() tea.Cmd {
	if m.tab != 2 || m.offline || m.busy || m.pairLoading {
		m.message = "Select a pair in Connections and wait for current changes before pairing."
		return nil
	}
	from, to := m.selectedID(), m.nativeDeliveryRecipient()
	if from == "" || to == "" || from == to || m.client == nil {
		m.message = "Choose two different owned native sessions before pairing for tasks."
		return nil
	}
	m.pairRequest++
	m.pairLoading, m.busy = true, true
	m.message = "Loading the exact pair's current permissions, delivery and queues."
	read := consoleTaskPairRead{state: m.state, from: from, to: to, request: m.pairRequest}
	client := m.client
	return func() tea.Msg {
		read.review, read.err = prepareWorkPairReview(context.Background(), client, from, to, "review")
		return read
	}
}

func (m *console) receiveTaskPairRead(read consoleTaskPairRead) {
	if read.state != m.state || read.request != m.pairRequest || !m.pairLoading {
		return
	}
	m.pairLoading, m.busy = false, false
	if m.tab != 2 || m.selectedID() != read.from || m.nativeDeliveryRecipient() != read.to ||
		m.composer != "" || m.reader != nil || m.ownerReview != nil || m.nativeAction != nil || m.teamAction != nil || m.workspacePicker != nil {
		m.message = "Pair selection changed; reopen task pairing for the current identities."
		return
	}
	if read.err != nil {
		m.message = "Task pairing unavailable: " + consoleContent(read.err.Error(), 500)
		return
	}
	m.ownerReview = taskPairOwnerReview(read.review)
}

func (m console) applyTaskPair(review consoleOwnerReview) tea.Cmd {
	client, state := m.client, m.state
	return func() tea.Msg {
		_, err := workCallRequest(context.Background(), client, review.operation, review.args, review.requestID)
		return consoleTaskPairApplied{state: state, review: review, err: err}
	}
}

func (m *console) receiveTaskPairApplied(applied consoleTaskPairApplied) {
	if applied.state != m.state {
		return
	}
	m.busy = false
	if applied.err == nil {
		m.message = applied.review.message
		return
	}
	var rejected *managed.Fault
	if errors.As(applied.err, &rejected) {
		switch rejected.Code {
		case "conflict", "invalid", "forbidden", "not_found", "limit", "expired", "unsupported", "unauthorized":
			m.message = "Task pairing rejected: " + consoleContent(applied.err.Error(), 300) + ". No pair settings changed. Refresh and reopen Shift+C; an older workspace controller may need an explicit upgrade."
			return
		}
	}
	m.message = "Task pairing was not confirmed. Inspect current settings before retrying; an older controller may need an upgrade."
	if m.ownerReview == nil && m.composer == "" && m.reader == nil && m.nativeAction == nil && m.teamAction == nil && m.workspacePicker == nil {
		review := applied.review
		review.body = fmt.Sprintf("Previous submission unconfirmed: %s\nNo automatic retry. Esc returns to inspect; the exact request is retained.\n\n%s", consoleContent(applied.err.Error(), 500), review.body)
		m.ownerReview = &review
	}
}
