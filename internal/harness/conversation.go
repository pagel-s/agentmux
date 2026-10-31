package harness

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Conversation selects explicit native persistence. Nil or Mode fresh preserves
// ephemeral adapter behavior. Dir is a dedicated private transcript directory,
// mounted narrowly by Wrap. Dir empty is trusted-local compatibility: the CLI
// retains its native host session storage and authentication, with no isolation
// claim. ID empty is allowed only for create; the actual ID is returned in Result.
type Conversation struct{ Mode, ID, Dir string }

type AdapterCapabilities struct{ PersistentConversations, PrivateSessionStorage bool }

func Capabilities(name string) AdapterCapabilities {
	switch name {
	case "claude", "claude-code", "codex", "pi":
		return AdapterCapabilities{true, true}
	default:
		return AdapterCapabilities{}
	}
}

var (
	ErrConversationUnsupported = errors.New("persistent conversations are unsupported for this harness")
	ErrConversationMissing     = errors.New("exact native conversation transcript is missing; refusing to start a fresh session")
	ErrConversationMismatch    = errors.New("native conversation reference did not match the requested persisted conversation")
)

func conversationTarget(name string) string {
	switch name {
	case "claude":
		return "/home/agent/.claude/projects"
	case "codex":
		return "/home/agent/.codex/sessions"
	case "pi":
		return "/home/agent/.pi/agent/sessions"
	}
	return ""
}

func newConversationID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[:4], value[4:6], value[6:8], value[8:10], value[10:]), nil
}

func validConversationID(name, id string) bool {
	if len(id) < 1 || len(id) > 128 {
		return false
	}
	if name != "pi" {
		if len(id) != 36 {
			return false
		}
		for i, c := range id {
			if i == 8 || i == 13 || i == 18 || i == 23 {
				if c != '-' {
					return false
				}
				continue
			}
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
		return true
	}
	for i, c := range id {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		if i != 0 && i != len(id)-1 && (c == '.' || c == '_' || c == '-') {
			continue
		}
		return false
	}
	return true
}

func configureConversation(req Request, spec *Spec, env map[string]string) error {
	if req.Conversation == nil {
		return nil
	}
	c := *req.Conversation
	if c.Mode == "" || c.Mode == "fresh" {
		if c.ID != "" || c.Dir != "" {
			return errors.New("fresh conversation must not supply a native ID or directory")
		}
		return nil
	}
	if !Capabilities(spec.Name).PersistentConversations {
		return ErrConversationUnsupported
	}
	if c.Mode != "create" && c.Mode != "resume" {
		return errors.New("conversation mode must be fresh, create, or resume")
	}
	for _, arg := range req.Command {
		flag := strings.SplitN(arg, "=", 2)[0]
		if flag == "--continue" || flag == "--resume" || flag == "--session-id" || flag == "--session" || flag == "--session-dir" || flag == "--no-session" || flag == "--no-session-persistence" || flag == "--ephemeral" || flag == "--fork-session" || flag == "--fork" || (spec.Name != "codex" && (flag == "-c" || flag == "-r")) {
			return errors.New("owner command conflicts with controller-managed native conversation selection")
		}
	}
	if c.Mode == "resume" && !validConversationID(spec.Name, c.ID) || c.ID != "" && !validConversationID(spec.Name, c.ID) {
		return errors.New("invalid exact native conversation ID")
	}
	if c.Mode == "create" && spec.Name == "codex" && c.ID != "" {
		return errors.New("Codex create learns its native ID from the event stream")
	}
	storage := c.Dir
	if c.Dir != "" {
		if req.Wrap == nil {
			return errors.New("private conversation storage requires an explicit sandbox wrapper")
		}
		canonical, err := filepath.EvalSymlinks(c.Dir)
		info, statErr := os.Lstat(c.Dir)
		if !filepath.IsAbs(c.Dir) || err != nil || statErr != nil || canonical != filepath.Clean(c.Dir) || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return errors.New("conversation directory must be a canonical private existing directory")
		}
		spec.SessionDirs = append(spec.SessionDirs, SessionDir{Source: c.Dir, Target: conversationTarget(spec.Name)})
		if spec.Name == "claude" {
			env["CLAUDE_CONFIG_DIR"] = "/home/agent/.claude"
		}
		if spec.Name == "codex" {
			env["CODEX_HOME"] = "/home/agent/.codex"
		}
	} else {
		home := env["HOME"]
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		switch spec.Name {
		case "claude":
			root := env["CLAUDE_CONFIG_DIR"]
			if root == "" {
				root = filepath.Join(home, ".claude")
			}
			c.Dir = filepath.Join(root, "projects")
		case "codex":
			root := env["CODEX_HOME"]
			if root == "" {
				root = filepath.Join(home, ".codex")
			}
			c.Dir = filepath.Join(root, "sessions")
		case "pi":
			c.Dir = filepath.Join(home, ".pi", "agent", "sessions")
		}
		storage = c.Dir
	}
	transcript := ""
	if c.ID != "" {
		var err error
		transcript, err = findConversationFile(spec.Name, c.Dir, c.ID)
		if c.Mode == "resume" && err != nil {
			return err
		}
		if c.Mode == "create" && err != nil && !errors.Is(err, ErrConversationMissing) {
			return err
		}
		if c.Mode == "create" && transcript != "" {
			return errors.New("conversation create ID already exists; explicit resume is required")
		}
	}
	if len(spec.SessionDirs) != 0 {
		storage = conversationTarget(spec.Name)
	}
	remove := func(flag string) {
		args := spec.Args[:0]
		for _, arg := range spec.Args {
			if arg != flag {
				args = append(args, arg)
			}
		}
		spec.Args = args
	}
	switch spec.Name {
	case "claude":
		remove("--no-session-persistence")
		if c.Mode == "create" && c.ID != "" {
			spec.Args = append(spec.Args, "--session-id", c.ID)
		} else if c.Mode == "resume" {
			spec.Args = append(spec.Args, "--resume", c.ID)
		}
	case "codex":
		if c.Mode == "resume" {
			spec.Args = append(spec.Args, "resume", c.ID)
		}
	case "pi":
		remove("--no-session")
		spec.Args = append(spec.Args, "--session-dir", storage)
		if c.Mode == "create" && c.ID != "" {
			spec.Args = append(spec.Args, "--session-id", c.ID)
		} else if c.Mode == "resume" {
			relative, _ := filepath.Rel(c.Dir, transcript)
			spec.Args = append(spec.Args, "--session", filepath.Join(storage, relative))
		}
	}
	spec.conversation = &c
	return nil
}

func findConversationFile(name, directory, id string) (string, error) {
	match := ""
	entries := 0
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		entries++
		if err != nil {
			return err
		}
		if entries > 10000 {
			return errors.New("conversation storage exceeds bounded reference lookup")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		base := entry.Name()
		matches := base == id+".jsonl" || name == "codex" && strings.HasSuffix(base, "-"+id+".jsonl") || name == "pi" && strings.HasSuffix(base, "_"+id+".jsonl")
		if matches {
			if match != "" {
				return errors.New("native conversation reference is ambiguous")
			}
			if info, err := entry.Info(); err != nil || !info.Mode().IsRegular() {
				return errors.New("native conversation transcript must be a regular file")
			}
			match = path
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if match == "" {
		return "", ErrConversationMissing
	}
	return match, nil
}

func verifyConversation(name string, conversation *Conversation, id string) error {
	if !validConversationID(name, id) || conversation.ID != "" && conversation.ID != id {
		return ErrConversationMismatch
	}
	_, err := findConversationFile(name, conversation.Dir, id)
	return err
}
