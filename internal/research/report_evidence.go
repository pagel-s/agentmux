package research

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

// Duplicate rows otherwise increase a condition's apparent N while overwriting
// the same paired block in the contrast calculation. Legacy reports may omit
// identifiers/evidence, but cannot contradict the identifiers they do declare.
func validateReportEntries(report Report) error {
	conditions := map[string]bool{}
	for _, condition := range report.Conditions {
		if condition.ID == "" || conditions[condition.ID] {
			return errors.New("duplicate or empty research condition")
		}
		conditions[condition.ID] = true
	}
	cells, trials, tasks := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, result := range report.Results {
		trial := result.Trial
		cell := fmt.Sprintf("%s/%d/%d", trial.Condition, trial.Seed, trial.Repetition)
		if cells[cell] || trial.ID != "" && trials[trial.ID] {
			return errors.New("duplicate research trial or condition/seed/repetition cell")
		}
		if len(conditions) > 0 && !conditions[trial.Condition] {
			return errors.New("research result references an undeclared condition")
		}
		cells[cell], trials[trial.ID] = true, true
		for _, phase := range result.Phases {
			for _, task := range phase.Tasks {
				if task.ID == "" {
					continue
				}
				if tasks[task.ID] {
					return errors.New("duplicate research task identity")
				}
				tasks[task.ID] = true
			}
		}
	}
	if report.Preregistration != nil {
		if err := report.Preregistration.validate(Manifest{Conditions: report.Conditions}); err != nil {
			return fmt.Errorf("invalid saved research preregistration: %w", err)
		}
	}
	return nil
}

// Read-only audits verify stored evidence without comparing executables to the
// current installation. Executable upgrades block new turns, not old analysis.
func verifyReportEvidence(dir string, report Report) error {
	var execution ExecutionEvidence
	err := privateJSON(filepath.Join(dir, "execution.json"), &execution)
	if os.IsNotExist(err) && report.Execution == nil {
		for _, result := range report.Results {
			if result.Protocol != "" {
				return errors.New("research result lacks its collection execution evidence")
			}
		}
		return nil // Explicitly lower-coverage legacy report.
	}
	if err != nil || report.Execution == nil || execution.Version != 1 || execution.Protocol != decisionProtocol || !reflect.DeepEqual(execution, *report.Execution) {
		return errors.New("research report execution evidence differs from its collection")
	}
	if err := checkStoredManifest(dir, report.ManifestHash); err != nil {
		return err
	}
	var stored struct {
		Manifest Manifest `json:"manifest"`
	}
	if err := privateJSON(filepath.Join(dir, "manifest.json"), &stored); err != nil {
		return err
	}
	if report.Research != stored.Manifest.ID || !reflect.DeepEqual(report.Conditions, stored.Manifest.Conditions) || !reflect.DeepEqual(report.Preregistration, stored.Manifest.Preregistration) {
		return errors.New("research report metadata differs from its frozen manifest")
	}
	plan, err := Plan(stored.Manifest)
	if err != nil {
		return err
	}
	if err := checkStoredPlan(dir, plan); err != nil {
		return err
	}
	budget, err := loadBudget(dir, stored.Manifest.Budget.MaxModelTasks)
	if err != nil || budget.Limit > 0 && budget.fresh {
		return errors.New("research budget evidence is unavailable")
	}
	planned := map[string]Trial{}
	for _, trial := range plan.Trials {
		planned[trial.ID] = trial
	}
	seen := map[string]bool{}
	for _, result := range report.Results {
		trial, ok := planned[result.Trial.ID]
		if !ok || trial != result.Trial {
			return errors.New("research result differs from its frozen plan")
		}
		if result.Status != "completed" {
			continue
		}
		attemptDir := filepath.Join(dir, trial.ID, fmt.Sprintf("attempt-%03d", result.Attempt))
		var checkpoint Result
		if err := privateJSON(filepath.Join(attemptDir, "result.json"), &checkpoint); err != nil || !reflect.DeepEqual(checkpoint, result) {
			return errors.New("research report differs from its completed trial checkpoint")
		}
		if err := checkCachedResult(stored.Manifest, trial, result.Attempt, attemptDir, result, budget, seen, true); err != nil {
			return err
		}
	}
	return nil
}
