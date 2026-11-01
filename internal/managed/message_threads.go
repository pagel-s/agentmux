package managed

import (
	"context"
	"strings"
)

// replyThread binds ancestry to an existing visible message. Referencing a
// private message in a broader audience would leak its identity (and commonly
// its quoted content), so every recipient must already be able to read it.
// Replies never grant access to an ancestor and cannot select their own root.
func (s *Store) replyThread(ctx context.Context, q queryer, a actor, reference string, outgoing Message) (string, *Fault) {
	var parent Message
	if strings.HasPrefix(reference, "task_") {
		t, err := loadTask(ctx, q, reference)
		if err != nil || !canSeeTask(a, t.Task) || get(ctx, q, "message", t.MessageID, &parent) != nil {
			return "", fault("not_found", "reply reference is not visible")
		}
	} else if get(ctx, q, "message", reference, &parent) != nil || !s.canSeeMessage(ctx, q, a, parent) {
		return "", fault("not_found", "reply reference is not visible")
	}
	if !s.canSeeMessage(ctx, q, a, parent) {
		return "", fault("not_found", "reply reference is not visible")
	}
	if outgoing.Board != "" {
		// A board's future readers cannot be enumerated safely. Retaining the
		// same board is the only automatic board audience-preserving reply.
		if parent.Board != outgoing.Board {
			return "", fault("forbidden", "reply must retain its original board or select existing readers as direct recipients")
		}
	} else {
		for _, id := range outgoing.To {
			if !s.canSeeMessage(ctx, q, actor{ID: id}, parent) {
				return "", fault("forbidden", "reply recipient cannot read the referenced message")
			}
		}
	}
	if parent.ThreadID != "" {
		return parent.ThreadID, nil
	}
	return parent.ID, nil
}
