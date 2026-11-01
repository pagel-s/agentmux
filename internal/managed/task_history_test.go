package managed

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestTaskHistorySearchPagingNeverWidensVisibility(t *testing.T) {
	cfg := testConfig()
	cfg.Limits.MaxTasks = 200
	s := testStore(t, cfg)
	ids := []string{}
	for i := 0; i < 85; i++ {
		to := "bob"
		if i%2 == 0 {
			to = "alice"
		}
		task := taskIn(t, call(t, s, "", "instruct", map[string]any{"to": []string{to}, "text": fmt.Sprintf("History item %03d needle-漢字", i)}))
		ids = append(ids, task.ID)
	}
	type page struct {
		Tasks  []Task `json:"tasks"`
		Cursor string `json:"cursor"`
		More   bool   `json:"has_more"`
	}
	after := ""
	seen := map[string]bool{}
	n := 84
	for {
		var p page
		raw := call(t, s, "", "task.list", map[string]any{"limit": 17, "order": "desc", "after": after, "query": "needle-漢字"})
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		for _, task := range p.Tasks {
			if n < 0 || task.ID != ids[n] || seen[task.ID] {
				t.Fatalf("wrong history order or duplicate: %s %d", raw, n)
			}
			seen[task.ID] = true
			n--
		}
		if !p.More {
			break
		}
		if p.Cursor == after || len(p.Tasks) == 0 {
			t.Fatal("history cursor did not advance")
		}
		after = p.Cursor
	}
	if len(seen) != 85 || n != -1 {
		t.Fatalf("lost history: %d %d", len(seen), n)
	}
	var found page
	if err := json.Unmarshal(call(t, s, "", "task.list", map[string]any{"query": "item 000", "order": "desc"}), &found); err != nil {
		t.Fatal(err)
	}
	if len(found.Tasks) != 1 || found.Tasks[0].ID != ids[0] {
		t.Fatalf("oldest search failed: %+v", found)
	}
	var restricted page
	if err := json.Unmarshal(call(t, s, "alice", "task.list", map[string]any{"order": "desc", "to": "bob", "query": "needle"}), &restricted); err != nil {
		t.Fatal(err)
	}
	if len(restricted.Tasks) != 0 {
		t.Fatal("search widened actor visibility")
	}
	expectFault(t, s, "alice", "task.list", "invalid", map[string]any{"order": "desc", "after": ids[1]})
	expectFault(t, s, "", "task.list", "invalid", map[string]any{"order": "wrong"})
}
