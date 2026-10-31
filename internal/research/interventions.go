package research

import (
	"context"
	"errors"
	"path/filepath"
	"slices"

	"agentmux/internal/managed"
)

const interventionProtocol = "agentmux-research-owner-interventions-v1"

type interventionEvidence struct {
	Protocol       string                         `json:"protocol"`
	Interventions  []managed.ResearchIntervention `json:"interventions"`
	OwnerMutations int                            `json:"owner_mutations"`
}

func verifyInterventions(dir string, result Result) error {
	if result.InterventionProtocol != interventionProtocol {
		return errors.New("unsupported research intervention evidence protocol")
	}
	var artifact interventionEvidence
	if err := privateJSON(filepath.Join(dir, "interventions.json"), &artifact); err != nil || artifact.Protocol != interventionProtocol || artifact.OwnerMutations != result.OwnerMutations || !slices.Equal(artifact.Interventions, result.Interventions) {
		return errors.New("research intervention artifact differs from its trial result")
	}
	store, err := managed.Open(filepath.Join(dir, "state"))
	if err != nil {
		return errors.New("research intervention broker evidence is unavailable")
	}
	defer store.Close()
	status, err := store.ResearchSnapshot(context.Background())
	if err != nil || status == nil || status.Progress.Trial != result.Trial.ID || status.Progress.Attempt != result.Attempt || status.Progress.State != "completed" || status.OwnerMutations != result.OwnerMutations || !slices.Equal(status.Control.Interventions, result.Interventions) {
		return errors.New("research intervention broker evidence differs from its completed trial")
	}
	return nil
}
