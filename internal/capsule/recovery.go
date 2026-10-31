package capsule

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type capsuleState struct {
	Volumes            []volume          `json:"volumes"`
	Before             map[string]string `json:"before"`
	MaxEntries         int               `json:"max_entries"`
	OtherHistorySHA256 string            `json:"other_history_sha256,omitempty"`
}

type RecoveryVolume struct {
	Name       string `json:"name"`
	Current    string `json:"current_sha256,omitempty"`
	Checkpoint string `json:"checkpoint_sha256,omitempty"`
	Previous   string `json:"previous_sha256,omitempty"`
	Before     string `json:"before_sha256,omitempty"`
	After      string `json:"after_sha256,omitempty"`
}
type RecoveryReport struct {
	Agent   string           `json:"agent"`
	Present bool             `json:"present"`
	State   string           `json:"state"`
	Digest  string           `json:"digest,omitempty"`
	Actions []string         `json:"actions"`
	Volumes []RecoveryVolume `json:"volumes,omitempty"`
	Note    string           `json:"note,omitempty"`
}
type recoveryView struct {
	report  RecoveryReport
	state   capsuleState
	receipt map[string]string
	journal bool
	dir     string
}

func smallMetadata(dir, name string) ([]byte, error) {
	p := filepath.Join(dir, name)
	info, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !regular(info) || info.Mode().Perm()&0077 != 0 || info.Size() > 65536 {
		return nil, errors.New("capsule metadata must be a bounded private regular file")
	}
	return os.ReadFile(p)
}
func decodeMetadata(data []byte, target any) error {
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("capsule metadata must contain one JSON object")
	}
	return nil
}
func sha(data []byte) string    { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func validDigest(v string) bool { b, e := hex.DecodeString(v); return e == nil && len(b) == 32 }
func snapshot(ctx context.Context, dir string, maxBytes int64, maxEntries int) (string, error) {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("capsule snapshot must be an unlinked directory")
	}
	s, err := inspectContext(ctx, dir, maxBytes, maxEntries, "")
	return s.Digest, err
}

func recoveryLocks(o Options) (*os.File, *os.File, string, error) {
	if err := validateOptions(o, true); err != nil {
		return nil, nil, "", err
	}
	worker, err := takeLock(filepath.Join(o.StateDir, "worker-"+o.AgentID+".lock"))
	if err != nil {
		return nil, nil, "", err
	}
	dir := filepath.Join(o.StateDir, "capsules", o.AgentID)
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return worker, nil, dir, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		worker.Close()
		return nil, nil, "", errors.New("capsule quarantine must be a private directory")
	}
	if canonical, e := filepath.EvalSymlinks(dir); e != nil || canonical != dir {
		worker.Close()
		return nil, nil, "", errors.New("capsule quarantine parents must not contain symlinks")
	}
	controller, err := takeLock(filepath.Join(dir, "controller.lock"))
	if err != nil {
		worker.Close()
		return nil, nil, "", err
	}
	return worker, controller, dir, nil
}

// Inspect is an owner-side filesystem operation. The caller must authenticate
// the owner before calling it. It holds the same lock as RunWorker, plus the
// capsule controller lock, so no active worker can race a recovery operation.
func Inspect(ctx context.Context, o Options) (RecoveryReport, error) {
	o.StateDir, _ = filepath.Abs(o.StateDir)
	o.Workspace, _ = filepath.Abs(o.Workspace)
	worker, controller, dir, err := recoveryLocks(o)
	if err != nil {
		return RecoveryReport{}, err
	}
	defer worker.Close()
	if controller != nil {
		defer controller.Close()
	}
	v, err := readRecovery(ctx, o, dir)
	return v.report, err
}

func readRecovery(ctx context.Context, o Options, dir string) (recoveryView, error) {
	v := recoveryView{dir: dir, report: RecoveryReport{Agent: o.AgentID, State: "absent", Actions: []string{}}}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	v.report.Present = true
	v.report.State = "prepared"
	metadata := map[string]string{}
	raw := map[string][]byte{}
	allowed := map[string]bool{"quarantine.json": true, "state.json": true, "manifest.json": true, "received.json": true, "publish.json": true, "output": true, "previous-0": true, "previous-1": true, "controller.lock": true, "checkpoint.sock": true}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return v, fmt.Errorf("unrecognized capsule resource requires manual inspection: %s", entry.Name())
		}
		if strings.HasSuffix(entry.Name(), ".json") {
			b, e := smallMetadata(dir, entry.Name())
			if e != nil {
				return v, e
			}
			raw[entry.Name()] = b
			metadata[entry.Name()] = sha(b)
		}
	}
	if len(raw["state.json"]) == 0 {
		for _, name := range []string{"previous-0", "previous-1", "publish.json", "received.json", "output"} {
			if _, e := os.Lstat(filepath.Join(dir, name)); e == nil {
				return v, errors.New("capsule has snapshots without recovery metadata; preserve quarantine")
			}
		}
		v.report.Actions = []string{"discard"}
		encoded, _ := json.Marshal(metadata)
		v.report.Digest = sha(encoded)
		return v, nil
	}
	if err = decodeMetadata(raw["state.json"], &v.state); err != nil {
		return v, err
	}
	if len(v.state.Before) != len(v.state.Volumes) {
		return v, errors.New("unexpected capsule baseline resources")
	}
	for i := len(v.state.Volumes); i < 2; i++ {
		if _, e := os.Lstat(filepath.Join(dir, "previous-"+strconv.Itoa(i))); e == nil {
			return v, errors.New("unexpected previous snapshot requires manual inspection")
		}
	}
	if len(v.state.Volumes) < 1 || len(v.state.Volumes) > 2 || v.state.MaxEntries < 1 || v.state.MaxEntries > o.MaxEntries {
		return v, errors.New("invalid capsule recovery manifest")
	}
	for i, volume := range v.state.Volumes {
		if !validDigest(v.state.Before[volume.Source]) || volume.Bytes < 0 {
			return v, errors.New("invalid capsule baseline digest or quota")
		}
		if i == 0 {
			if volume.Name != "workspace" || volume.Source != o.Workspace || volume.Bytes != o.WorkspaceBytes {
				return v, errors.New("capsule workspace identity mismatch")
			}
		} else {
			root := filepath.Join(o.StateDir, "sessions", o.AgentID)
			canonical, e := filepath.EvalSymlinks(root)
			info, statErr := os.Stat(root)
			if e != nil || canonical != root || statErr != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
				return v, errors.New("capsule history parents must be canonical private directories")
			}
			relative, e := filepath.Rel(root, volume.Source)
			if e != nil || relative == "." || strings.Contains(relative, string(filepath.Separator)) || !strings.HasPrefix(relative, "g-") || volume.Name != "history/0" || volume.Bytes > o.HistoryBytes {
				return v, errors.New("capsule history identity mismatch")
			}
		}
	}
	if b := raw["received.json"]; len(b) > 0 {
		if err = decodeMetadata(b, &v.receipt); err != nil {
			return v, err
		}
		if len(v.receipt) != len(v.state.Volumes) {
			return v, errors.New("invalid checkpoint acceptance receipt")
		}
		for _, volume := range v.state.Volumes {
			if !validDigest(v.receipt[volume.Name]) {
				return v, errors.New("invalid accepted checkpoint digest")
			}
		}
		v.report.State = "checkpointed"
	}
	v.journal = len(raw["publish.json"]) > 0
	if v.journal {
		var j struct {
			State   string `json:"state"`
			Volumes []struct {
				Name   string `json:"name"`
				Source string `json:"source"`
				Before string `json:"before_sha256"`
				After  string `json:"after_sha256"`
			} `json:"volumes"`
		}
		if err = decodeMetadata(raw["publish.json"], &j); err != nil {
			return v, err
		}
		if len(j.Volumes) != len(v.state.Volumes) || j.State != "publishing" {
			return v, errors.New("invalid publication journal")
		}
		for i, jv := range j.Volumes {
			sv := v.state.Volumes[i]
			if jv.Name != sv.Name || jv.Source != sv.Source || jv.Before != v.state.Before[sv.Source] || jv.After != v.receipt[sv.Name] {
				return v, errors.New("publication journal does not match accepted checkpoint")
			}
		}
		v.report.State = "publishing"
	}
	conflict := false
	discard, restore, publish := true, true, len(v.receipt) > 0
	for i, volume := range v.state.Volumes {
		s := RecoveryVolume{Name: volume.Name, Before: v.state.Before[volume.Source], After: v.receipt[volume.Name]}
		s.Current, err = snapshot(ctx, volume.Source, volume.Bytes, o.MaxEntries)
		if err != nil {
			return v, err
		}
		s.Checkpoint, err = snapshot(ctx, filepath.Join(dir, "output", filepath.FromSlash(volume.Name)), volume.Bytes, o.MaxEntries)
		if err != nil {
			return v, err
		}
		s.Previous, err = snapshot(ctx, filepath.Join(dir, "previous-"+strconv.Itoa(i)), volume.Bytes, o.MaxEntries)
		if err != nil {
			return v, err
		}
		if s.Current != s.Before {
			discard = false
		}
		if s.Previous != "" && s.Previous != s.Before {
			discard = false
			restore = false
			publish = false
			conflict = true
		}
		if s.Current != s.Before && (s.Current != "" && s.Current != s.After || s.Previous != s.Before) {
			restore = false
			conflict = true
		}
		if s.Checkpoint != "" && s.After != "" && s.Checkpoint != s.After {
			restore = false
			publish = false
			discard = false
			conflict = true
		}
		validPublish := s.Current == s.Before && s.Checkpoint == s.After && s.Previous == "" || s.Current == "" && s.Checkpoint == s.After && s.Previous == s.Before || s.Current == s.After && s.Checkpoint == "" && s.Previous == s.Before
		publish = publish && validPublish
		v.report.Volumes = append(v.report.Volumes, s)
	}
	otherDigest := ""
	if len(v.state.Volumes) > 1 {
		volume := v.state.Volumes[1]
		other, e := inspectContext(ctx, filepath.Join(o.StateDir, "sessions", o.AgentID), o.HistoryBytes, o.MaxEntries, filepath.Base(volume.Source))
		if e != nil {
			return v, e
		}
		otherDigest = other.Digest
		if other.Digest != v.state.OtherHistorySHA256 {
			discard = false
			restore = false
			publish = false
			conflict = true
		}
	}
	// Reject unrecognized staging subtrees rather than deleting potentially
	// unique owner files merely because their names were not in the manifest.
	if err = validateOutputLayout(dir, len(v.state.Volumes)); err != nil {
		return v, err
	}
	if discard {
		v.report.Actions = append(v.report.Actions, "discard")
	}
	if restore && (v.journal || !discard) {
		v.report.Actions = append(v.report.Actions, "restore")
	}
	if publish {
		v.report.Actions = append(v.report.Actions, "publish")
	}
	if conflict {
		v.report.State = "conflict"
		v.report.Note = "Snapshot or other-generation data changed; automatic recovery will not overwrite it."
	}
	encoded, _ := json.Marshal(struct {
		Metadata map[string]string
		Volumes  []RecoveryVolume
		Other    string
	}{metadata, v.report.Volumes, otherDigest})
	v.report.Digest = sha(encoded)
	return v, nil
}

func validateOutputLayout(dir string, volumes int) error {
	root := filepath.Join(dir, "output")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() != "workspace" && (entry.Name() != "history" || volumes < 2) {
			return errors.New("unrecognized checkpoint subtree requires manual inspection")
		}
		if entry.Name() == "history" {
			children, e := os.ReadDir(filepath.Join(root, "history"))
			if e != nil {
				return e
			}
			for _, child := range children {
				if !child.IsDir() || child.Name() != "0" {
					return errors.New("unrecognized history subtree requires manual inspection")
				}
			}
		}
	}
	return nil
}

// Recover never infers native task success. It resolves only the selected
// filesystem snapshots; the owner must separately resolve uncertain leases and
// conversation state. A current Inspect digest is mandatory.
func Recover(ctx context.Context, o Options, action, expected string) (RecoveryReport, error) {
	o.StateDir, _ = filepath.Abs(o.StateDir)
	o.Workspace, _ = filepath.Abs(o.Workspace)
	if action != "discard" && action != "restore" && action != "publish" {
		return RecoveryReport{}, errors.New("recovery action must be discard, restore or publish")
	}
	if !validDigest(expected) {
		return RecoveryReport{}, errors.New("recovery requires the current inspect digest")
	}
	worker, controller, dir, err := recoveryLocks(o)
	if err != nil {
		return RecoveryReport{}, err
	}
	defer worker.Close()
	if controller != nil {
		defer controller.Close()
	}
	v, err := readRecovery(ctx, o, dir)
	if err != nil {
		return v.report, err
	}
	if !v.report.Present {
		return v.report, nil
	}
	if v.report.Digest != expected {
		return v.report, errors.New("capsule changed since inspection")
	}
	allowed := false
	for _, a := range v.report.Actions {
		allowed = allowed || a == action
	}
	if !allowed {
		return v.report, errors.New("requested action is unsafe for these snapshots; preserve quarantine")
	}
	if err = ctx.Err(); err != nil {
		return v.report, err
	}
	if action == "publish" && !v.journal {
		j := struct {
			State   string              `json:"state"`
			Volumes []map[string]string `json:"volumes"`
		}{State: "publishing"}
		for _, volume := range v.state.Volumes {
			j.Volumes = append(j.Volumes, map[string]string{"name": volume.Name, "source": volume.Source, "before_sha256": v.state.Before[volume.Source], "after_sha256": v.receipt[volume.Name]})
		}
		data, _ := json.Marshal(j)
		if err = writeDurable(filepath.Join(dir, "publish.json"), data); err != nil {
			return v.report, err
		}
	}
	if action != "discard" {
		for i, volume := range v.state.Volumes {
			s := v.report.Volumes[i]
			stage := filepath.Join(dir, "output", filepath.FromSlash(volume.Name))
			previous := filepath.Join(dir, "previous-"+strconv.Itoa(i))
			if action == "publish" {
				if s.Current == s.After && s.Checkpoint == "" {
					continue
				}
				if s.Current == s.Before {
					if err = os.Rename(volume.Source, previous); err != nil {
						return v.report, err
					}
					actual, e := snapshot(ctx, previous, volume.Bytes, o.MaxEntries)
					if e != nil || actual != s.Before {
						return v.report, errors.New("capsule changed during publish; previous snapshot preserved")
					}
				}
				if err = os.Rename(stage, volume.Source); err != nil {
					return v.report, err
				}
			} else {
				if s.Current == s.Before {
					continue
				}
				if s.Current != "" {
					if err = os.Rename(volume.Source, stage); err != nil {
						return v.report, err
					}
				}
				if err = os.Rename(previous, volume.Source); err != nil {
					return v.report, err
				}
			}
		}
	}
	for i, volume := range v.state.Volumes {
		want := v.report.Volumes[i].Before
		if action == "publish" {
			want = v.report.Volumes[i].After
		}
		actual, e := snapshot(ctx, volume.Source, volume.Bytes, o.MaxEntries)
		if e != nil || actual != want {
			return v.report, errors.New("recovery snapshot verification failed; preserve quarantine")
		}
		if err = syncDirectory(filepath.Dir(volume.Source)); err != nil {
			return v.report, err
		}
	}
	if err = os.RemoveAll(dir); err != nil {
		return v.report, err
	}
	if err = syncDirectory(filepath.Dir(dir)); err != nil {
		return v.report, err
	}
	return RecoveryReport{Agent: o.AgentID, State: action + "ed", Actions: []string{}}, nil
}

func takeLock(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		f.Close()
		return nil, errors.New("capsule lock must be a private regular file")
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("worker or capsule controller is still active")
	}
	return f, nil
}
