package research

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"agentmux/internal/managed"
)

type StudyTrial struct {
	Trial     string `json:"trial"`
	Condition string `json:"condition"`
	Attempt   int    `json:"attempt"`
	State     string `json:"state"`
}
type StudyView struct {
	AttemptID    string                  `json:"attempt_id,omitempty"`
	Output       string                  `json:"output"`
	CurrentState string                  `json:"current_state,omitempty"`
	Live         bool                    `json:"live"`
	Completed    int                     `json:"completed"`
	Total        int                     `json:"total"`
	State        string                  `json:"state"`
	Progress     *managed.ResearchStatus `json:"progress,omitempty"`
	Results      []StudyTrial            `json:"results"`
}

// StudyOutput accepts a private study directory or its exact run/attempt/state.
// Cached metadata never authorizes mutation of a different broker.
func StudyOutput(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if filepath.Base(abs) == "state" && strings.HasPrefix(filepath.Base(filepath.Dir(abs)), "attempt-") && strings.HasPrefix(filepath.Base(filepath.Dir(filepath.Dir(abs))), "run-") {
		abs = filepath.Dir(filepath.Dir(filepath.Dir(abs)))
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("study output must be a private directory without symlinks")
	}
	var stored struct {
		SHA256 string `json:"sha256"`
	}
	if err := privateJSON(filepath.Join(abs, "manifest.json"), &stored); err != nil {
		return "", err
	}
	if err := checkStoredManifest(abs, stored.SHA256); err != nil {
		return "", err
	}
	return abs, nil
}

func StudyStatus(ctx context.Context, path string) (StudyView, error) {
	root, err := StudyOutput(path)
	if err != nil {
		return StudyView{}, err
	}
	plan, err := verifiedStudyPlan(root)
	if err != nil {
		return StudyView{}, err
	}
	view := StudyView{Output: root, Total: len(plan.Trials), State: "waiting", Results: []StudyTrial{}}
	for _, trial := range plan.Trials {
		if !identifier.MatchString(trial.ID) {
			return view, errors.New("invalid study trial identity")
		}
		row := StudyTrial{Trial: trial.ID, Condition: trial.Condition, State: "pending"}
		nums, e := attemptNumbers(filepath.Join(root, trial.ID))
		if e != nil && !os.IsNotExist(e) {
			return view, e
		}
		if len(nums) > 0 {
			row.Attempt = nums[len(nums)-1]
			dir := filepath.Join(root, trial.ID, fmt.Sprintf("attempt-%03d", row.Attempt))
			if err := privateStudyChild(root, dir); err != nil {
				return view, err
			}
			var r Result
			if e := privateJSON(filepath.Join(dir, "result.json"), &r); e == nil {
				if r.Trial.ID != trial.ID || r.Attempt != row.Attempt {
					return view, errors.New("study result identity mismatch")
				}
				row.State = r.Status
				if r.Status == "completed" {
					view.Completed++
				}
			} else if os.IsNotExist(e) {
				row.State = "incomplete"
				view.State = "incomplete"
			} else {
				return view, e
			}
		}
		view.Results = append(view.Results, row)
	}
	if live, e := LiveStatus(ctx, root); e == nil {
		view.Live, view.Progress = live.Live, live.Research
		if live.Research != nil {
			view.CurrentState = live.Research.Progress.StateDir
			view.AttemptID = AttemptIdentity(view.CurrentState, live.Research)
			if live.Live {
				view.State = live.Research.Progress.State
			} else {
				switch live.Research.Progress.State {
				case "stopped", "interrupted":
					view.State = live.Research.Progress.State
				case "completed":
					view.State = "waiting_for_next_attempt"
				default:
					view.State = "unavailable"
				}
			}
			for i := range view.Results {
				if view.Results[i].Trial == live.Research.Progress.Trial && view.Results[i].Attempt == live.Research.Progress.Attempt && view.Results[i].State == "incomplete" {
					if live.Live || view.State == "stopped" || view.State == "interrupted" {
						view.Results[i].State = live.Research.Progress.State
					}
				}
			}
		}
	} else if !os.IsNotExist(e) {
		return view, e
	}
	if view.Completed == view.Total && view.Total > 0 {
		view.State = "completed"
		view.Live = false
	}
	return view, nil
}

// Observation uses the same frozen trial matrix as continuation and analysis.
// A readable plan is not enough: omitted, reordered or relabeled trials would
// otherwise change completion counts and the meaning of archive cursors.
func verifiedStudyPlan(root string) (PlanResult, error) {
	var stored struct {
		Manifest Manifest `json:"manifest"`
	}
	if err := privateJSON(filepath.Join(root, "manifest.json"), &stored); err != nil {
		return PlanResult{}, err
	}
	plan, err := Plan(stored.Manifest)
	if err != nil {
		return PlanResult{}, err
	}
	if err := checkStoredPlan(root, plan); err != nil {
		return PlanResult{}, err
	}
	return plan, nil
}

func AttemptIdentity(state string, status *managed.ResearchStatus) string {
	if status == nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", state, status.Progress.Trial, status.Progress.Attempt))))
}

func privateStudyChild(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("study path escapes output")
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, e := os.Lstat(current)
		if e != nil {
			return e
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
			return errors.New("study evidence directories must be private without symlinks")
		}
	}
	return nil
}

type HistoryFilter struct {
	After string
	Limit int
	Actor string
	Type  string
	Query string
}
type HistoryRecord struct {
	Sequence int                  `json:"sequence"`
	Trial    string               `json:"trial"`
	Attempt  int                  `json:"attempt"`
	Record   managed.ExportRecord `json:"record"`
}
type HistoryPage struct {
	Records []HistoryRecord `json:"records"`
	Cursor  string          `json:"cursor"`
	HasMore bool            `json:"has_more"`
}

// Recorded ordering is evidence ordering, not proof of knowledge or causation.
func ReadHistory(path string, filter HistoryFilter) (HistoryPage, error) {
	page := HistoryPage{Records: []HistoryRecord{}}
	root, err := StudyOutput(path)
	if err != nil {
		return page, err
	}
	after := 0
	if filter.After != "" {
		after, err = strconv.Atoi(filter.After)
		if err != nil || after < 0 {
			return page, errors.New("invalid history cursor")
		}
	}
	limit := filter.Limit
	if limit < 1 || limit > 200 {
		limit = 100
	}
	plan, err := verifiedStudyPlan(root)
	if err != nil {
		return page, err
	}
	sequence := 0
	for _, trial := range plan.Trials {
		if !identifier.MatchString(trial.ID) {
			return page, errors.New("invalid trial identity")
		}
		nums, e := attemptNumbers(filepath.Join(root, trial.ID))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return page, e
		}
		for _, attempt := range nums {
			dir := filepath.Join(root, trial.ID, fmt.Sprintf("attempt-%03d", attempt))
			if e := privateStudyChild(root, dir); e != nil {
				return page, e
			}
			path := filepath.Join(dir, "coordination.jsonl")
			info, e := os.Lstat(path)
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return page, e
			}
			if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 256<<20 {
				return page, errors.New("history must be a bounded private regular file")
			}
			f, e := os.Open(path)
			if e != nil {
				return page, e
			}
			scan := bufio.NewScanner(f)
			scan.Buffer(make([]byte, 4096), 4<<20)
			for scan.Scan() {
				sequence++
				if sequence <= after {
					continue
				}
				var record managed.ExportRecord
				if json.Unmarshal(scan.Bytes(), &record) != nil {
					f.Close()
					return page, errors.New("invalid archived coordination record")
				}
				var fields map[string]json.RawMessage
				_ = json.Unmarshal(record.Data, &fields)
				var eventType string
				_ = json.Unmarshal(fields["type"], &eventType)
				if filter.Type != "" && record.Kind != filter.Type && eventType != filter.Type {
					continue
				}
				if filter.Actor != "" {
					matched := false
					for _, key := range []string{"actor", "from", "agent", "id", "owner", "reader", "author"} {
						var value string
						_ = json.Unmarshal(fields[key], &value)
						matched = matched || value == filter.Actor
					}
					if !matched {
						continue
					}
				}
				if filter.Query != "" && !strings.Contains(strings.ToLower(string(scan.Bytes())), strings.ToLower(filter.Query)) {
					continue
				}
				if len(page.Records) == limit {
					page.HasMore = true
					f.Close()
					return page, nil
				}
				page.Records = append(page.Records, HistoryRecord{Sequence: sequence, Trial: trial.ID, Attempt: attempt, Record: record})
				page.Cursor = strconv.Itoa(sequence)
			}
			e = scan.Err()
			f.Close()
			if e != nil {
				return page, e
			}
		}
	}
	return page, nil
}
