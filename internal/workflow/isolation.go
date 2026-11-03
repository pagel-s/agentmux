package workflow

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"agentmux/internal/agentauth"
	"agentmux/internal/agybridge"
	"agentmux/internal/egress"
	"agentmux/internal/harness"
	"agentmux/internal/managedconfig"
	"agentmux/internal/managedsetup"
	"agentmux/internal/sandbox"
)

// IsolationOptions is an explicit capability grant, never an implicit discovery
// of the owner's login or home. Checks receive neither provider auth nor egress.
type IsolationOptions struct {
	Mode              string                   `json:"mode"`
	Network           string                   `json:"network,omitempty"`
	Allow             []egress.Endpoint        `json:"allow,omitempty"`
	ReadOnlyPaths     []string                 `json:"read_only_paths,omitempty"`
	Auth              map[string]IsolationAuth `json:"auth,omitempty"`
	HomeBytes         int64                    `json:"home_bytes,omitempty"`
	TempBytes         int64                    `json:"temp_bytes,omitempty"`
	BridgeMaxRequests int                      `json:"bridge_max_requests,omitempty"`
	BridgeCommand     []string                 `json:"bridge_command,omitempty"`
	RelayExecutable   string                   `json:"relay_executable,omitempty"`
}

type IsolationAuth struct {
	File     string `json:"file"`
	Provider string `json:"provider,omitempty"`
}

type isolationContextKey struct{}

type isolationBridge struct {
	base, key string
	server    *http.Server
	listener  net.Listener
	bridge    *agybridge.Bridge
}

type isolationRuntime struct {
	opts              IsolationOptions
	run, source       string
	metadataOnly      bool
	inputs            map[string]bool
	sourceGitPaths    []string
	protectedGitPaths []string
	relay             string
	bridges           map[string]isolationBridge
	mu                sync.Mutex
	secrets           map[string][]string
	credentials       *isolationCredentials
}

// NormalizedIsolation copies reference metadata without credential contents.
func NormalizedIsolation(input *IsolationOptions) *IsolationOptions {
	if input == nil {
		return nil
	}
	value := *input
	if value.Mode == "" {
		value.Mode = "bubblewrap"
	}
	if value.Network == "" {
		value.Network = "none"
	}
	if value.HomeBytes == 0 {
		value.HomeBytes = 64 << 20
	}
	if value.TempBytes == 0 {
		value.TempBytes = 64 << 20
	}
	if value.BridgeMaxRequests == 0 {
		value.BridgeMaxRequests = 32
	}
	value.ReadOnlyPaths = append([]string(nil), value.ReadOnlyPaths...)
	value.BridgeCommand = append([]string(nil), value.BridgeCommand...)
	value.Allow = append([]egress.Endpoint(nil), value.Allow...)
	for i := range value.Allow {
		value.Allow[i].IPs = append([]string(nil), value.Allow[i].IPs...)
	}
	if input.Auth != nil {
		value.Auth = make(map[string]IsolationAuth, len(input.Auth))
		for id, auth := range input.Auth {
			value.Auth[id] = auth
		}
	}
	return &value
}

func validateIsolationOptions(input *IsolationOptions) error {
	if input == nil {
		return nil
	}
	o := NormalizedIsolation(input)
	if runtime.GOOS != "linux" || o.Mode != "bubblewrap" {
		return errors.New("workflow isolation requires bubblewrap mode on Linux")
	}
	if o.Network != "none" && o.Network != "allowlist" || o.Network == "none" && len(o.Allow) != 0 {
		return errors.New("workflow isolation network must be none or an exact-origin allowlist")
	}
	if len(o.Allow) > 0 {
		if err := egress.Validate(o.Allow); err != nil {
			return err
		}
	}
	for _, size := range []int64{o.HomeBytes, o.TempBytes} {
		if size < 1<<20 || size > 1<<40 {
			return errors.New("isolated home/temp sizes must be 1 MiB–1 TiB")
		}
	}
	if len(o.ReadOnlyPaths) > 64 || len(o.Auth) > 16 || len(o.BridgeCommand) > 64 || o.BridgeMaxRequests < 1 || o.BridgeMaxRequests > 10000 {
		return errors.New("workflow isolation capability configuration exceeds its bounds")
	}
	for _, path := range append(append([]string(nil), o.ReadOnlyPaths...), o.RelayExecutable) {
		if path != "" && (!filepath.IsAbs(path) || strings.ContainsRune(path, 0)) {
			return errors.New("isolation dependencies require absolute valid paths")
		}
	}
	for _, argument := range o.BridgeCommand {
		if strings.ContainsRune(argument, 0) || len(argument) > 16<<10 {
			return errors.New("invalid inference bridge command argument")
		}
	}
	for _, auth := range o.Auth {
		if !filepath.IsAbs(auth.File) || strings.ContainsRune(auth.File, 0) || len(auth.Provider) > 128 {
			return errors.New("isolated authentication requires an explicitly selected absolute file")
		}
		if sandbox.RuntimeExposes(auth.File) {
			return errors.New("selected authentication must be outside exposed system runtime paths")
		}
		for _, dependency := range o.ReadOnlyPaths {
			if pathWithin(dependency, auth.File) {
				return errors.New("read-only dependency would expose an original authentication file")
			}
		}
	}
	return nil
}

// ValidateIsolation validates policy without loading credentials or starting a
// provider. A later real namespace/capacity probe is mandatory at execution.
func ValidateIsolation(p Plan) error {
	if err := validateIsolationOptions(p.Isolation); err != nil || p.Isolation == nil {
		return err
	}
	o := NormalizedIsolation(p.Isolation)
	source, sourceErr := filepath.Abs(p.Repository)
	if sourceErr != nil {
		return sourceErr
	}
	if resolved, err := filepath.EvalSymlinks(source); err == nil {
		source = resolved
	}
	if sandbox.RuntimeOverlaps(source) {
		return errors.New("isolated source repository must not overlap system runtime mounts")
	}
	if p.Storage != StoragePrivate {
		return errors.New("filesystem/network isolation requires private snapshot storage")
	}
	ids := map[string]Candidate{}
	bridge := false
	for _, candidate := range p.Candidates {
		if !workflowID.MatchString(candidate.ID) {
			return errors.New("isolated candidate requires a valid unique ID")
		}
		if _, duplicate := ids[candidate.ID]; duplicate {
			return errors.New("isolated candidate IDs must be unique")
		}
		ids[candidate.ID] = candidate
		if candidate.Harness == "antigravity" {
			return errors.New("native Antigravity login is not exported into isolation; select antigravity-bridge for isolated Gemini tools")
		}
		if candidate.Harness == "antigravity-bridge" {
			bridge = true
			if !strings.HasPrefix(candidate.Model, "gemini-") || o.Network != "allowlist" {
				return errors.New("isolated Antigravity bridge requires an explicit Gemini model and allowlist networking")
			}
		}
	}
	if o.Network == "allowlist" && len(o.Allow) == 0 && !bridge {
		return errors.New("allowlist networking requires at least one exact origin")
	}
	for id := range o.Auth {
		auth := o.Auth[id]
		canonical := auth.File
		if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
			canonical = resolved
		}
		if pathWithin(source, auth.File) || pathWithin(source, canonical) {
			return errors.New("selected authentication must be outside the source repository so snapshots and checks cannot inherit the original login")
		}
		for _, dependency := range o.ReadOnlyPaths {
			resolved := dependency
			if path, err := filepath.EvalSymlinks(dependency); err == nil {
				resolved = path
			}
			if pathWithin(resolved, auth.File) || pathWithin(resolved, canonical) {
				return errors.New("read-only dependency would expose an original authentication file")
			}
		}
		candidate, ok := ids[id]
		if !ok || candidate.Harness == "command" || candidate.Harness == "antigravity-bridge" {
			return errors.New("authentication selection must name one supported native candidate")
		}
		if candidate.APIKeyEnv != "" {
			return errors.New("choose one isolated credential source per candidate: auth file or selected API environment")
		}
	}
	return nil
}

type isolatedSourceWriteContextKey struct{}

// AdmitIsolatedSourceWrite is reserved for trusted integration after patch,
// approval, freshness and durable-intent checks. Native/check wrappers never
// consume this controller-only capability.
func AdmitIsolatedSourceWrite(ctx context.Context, repository string) (context.Context, error) {
	r := isolationFromContext(ctx)
	if r == nil {
		return ctx, nil
	}
	canonical, err := filepath.EvalSymlinks(repository)
	if err != nil || canonical != r.source {
		return ctx, errors.New("isolated source-write admission must name the exact original repository")
	}
	lease, ok := ctx.Value(applicationLeaseContextKey{}).(*os.File)
	if !ok || lease == nil {
		return ctx, errors.New("isolated source-write admission requires the active integration lease")
	}
	if info, err := lease.Stat(); err != nil || !info.Mode().IsRegular() {
		return ctx, errors.New("isolated source-write integration lease is unavailable")
	}
	return context.WithValue(ctx, isolatedSourceWriteContextKey{}, canonical), nil
}

func isolationFromContext(ctx context.Context) *isolationRuntime {
	value, _ := ctx.Value(isolationContextKey{}).(*isolationRuntime)
	return value
}

func IsolationFromContext(ctx context.Context) *IsolationOptions {
	if r := isolationFromContext(ctx); r != nil {
		return NormalizedIsolation(&r.opts)
	}
	return nil
}

func IsolationMode(ctx context.Context) string {
	if r := isolationFromContext(ctx); r != nil {
		return r.opts.Mode
	}
	return ""
}

type IsolationBridgeUsage struct {
	Limit    int   `json:"limit"`
	Admitted int64 `json:"admitted"`
	Rejected int64 `json:"rejected"`
}

// IsolationUsage reports finite native request admission; it is not a bill cap.
func IsolationUsage(ctx context.Context) map[string]IsolationBridgeUsage {
	r := isolationFromContext(ctx)
	if r == nil || len(r.bridges) == 0 {
		return nil
	}
	usage := map[string]IsolationBridgeUsage{}
	for id, bridge := range r.bridges {
		seen := bridge.bridge.Requests()
		admitted := min(seen, int64(r.opts.BridgeMaxRequests))
		usage[id] = IsolationBridgeUsage{Limit: r.opts.BridgeMaxRequests, Admitted: admitted, Rejected: seen - admitted}
	}
	return usage
}

// PrepareIsolation owns policy services for exactly the workflow lifetime.
// The Antigravity service is inference-only trusted host code: all model tools
// execute in isolated Pi, and no keyring, DBus socket or native home is mounted.
func PrepareIsolation(ctx context.Context, p Plan, runDir string) (context.Context, func(), error) {
	if existing := isolationFromContext(ctx); existing != nil {
		if err := compatibleInheritedIsolation(existing, p.Isolation); err != nil {
			return ctx, func() {}, err
		}
		target, err := filepath.EvalSymlinks(runDir)
		if err != nil {
			return ctx, func() {}, err
		}
		source, err := filepath.EvalSymlinks(p.Repository)
		if err != nil {
			return ctx, func() {}, err
		}
		if target == existing.run && source == existing.source && !existing.metadataOnly {
			return ctx, func() {}, nil
		}
		if !pathWithin(existing.run, target) || (!existing.inputs[source] && !pathWithin(existing.run, source)) {
			return ctx, func() {}, errors.New("nested isolation cannot expand the parent's private run or source inputs")
		}
		opts := p.Isolation
		if opts == nil {
			opts = &existing.opts
		}
		return prepareIsolation(ctx, opts, source, target, p.Candidates, false, existing)
	}
	if p.Isolation == nil {
		return ctx, func() {}, nil
	}
	if err := ValidateIsolation(p); err != nil {
		return ctx, func() {}, err
	}
	return prepareIsolation(ctx, p.Isolation, p.Repository, runDir, p.Candidates, false, nil)
}

func PrepareReportIsolation(ctx context.Context, r Report) (context.Context, func(), error) {
	if existing := isolationFromContext(ctx); existing != nil {
		if err := compatibleInheritedIsolation(existing, r.Isolation); err != nil {
			return ctx, func() {}, err
		}
		target, err := filepath.EvalSymlinks(r.Output)
		if err != nil {
			return ctx, func() {}, err
		}
		source, err := filepath.Abs(r.Repository)
		if err != nil {
			return ctx, func() {}, err
		}
		if resolved, e := filepath.EvalSymlinks(source); e == nil {
			source = resolved
		}
		if target == existing.run && source == existing.source {
			return ctx, func() {}, nil
		}
		if !pathWithin(existing.run, target) || (!existing.inputs[source] && !pathWithin(existing.run, source)) {
			return ctx, func() {}, errors.New("report isolation cannot expand the parent's private run or source inputs")
		}
		opts := r.Isolation
		if opts == nil {
			opts = &existing.opts
		}
		return prepareIsolation(ctx, opts, source, target, nil, true, existing)
	}
	if r.Isolation == nil {
		return ctx, func() {}, nil
	}
	if err := validateIsolationOptions(r.Isolation); err != nil {
		return ctx, func() {}, err
	}
	return prepareIsolation(ctx, r.Isolation, r.Repository, r.Output, nil, true, nil)
}

func compatibleInheritedIsolation(existing *isolationRuntime, requested *IsolationOptions) error {
	if requested == nil {
		return nil
	}
	left, _ := json.Marshal(NormalizedIsolation(&existing.opts))
	right, _ := json.Marshal(NormalizedIsolation(requested))
	if !bytes.Equal(left, right) {
		return errors.New("nested workflow must explicitly inherit the same isolation capability policy; requested changes cannot be silently weakened or expanded")
	}
	return nil
}

func prepareIsolation(ctx context.Context, opts *IsolationOptions, source, runDir string, candidates []Candidate, metadataOnly bool, parent *isolationRuntime) (context.Context, func(), error) {
	if sandbox.RuntimeOverlaps(source) || sandbox.RuntimeOverlaps(runDir) {
		return ctx, func() {}, errors.New("isolated source and private run must not overlap system runtime mounts")
	}
	if parent != nil {
		for _, path := range parent.protectedGitPaths {
			if sandbox.RuntimeOverlaps(path) {
				return ctx, func() {}, errors.New("protected original Git metadata must not overlap system runtime mounts")
			}
		}
	}
	if err := sandbox.ProbeWorkflow(ctx); err != nil {
		return ctx, func() {}, fmt.Errorf("required workflow isolation is unavailable: %w", err)
	}
	canonicalRun, err := filepath.EvalSymlinks(runDir)
	info, statErr := os.Lstat(runDir)
	if err != nil || statErr != nil || canonicalRun != runDir || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return ctx, func() {}, errors.New("isolation requires canonical private run state")
	}
	canonicalSource, err := filepath.Abs(source)
	if err != nil {
		return ctx, func() {}, err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(canonicalSource); resolveErr == nil {
		canonicalSource = resolved
	} else if !errors.Is(resolveErr, os.ErrNotExist) {
		return ctx, func() {}, resolveErr
	}
	sourceGitPaths, err := isolatedSourceGitPaths(ctx, canonicalSource, metadataOnly)
	if err != nil {
		return ctx, func() {}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	r := &isolationRuntime{opts: *NormalizedIsolation(opts), source: canonicalSource, run: canonicalRun, bridges: map[string]isolationBridge{}, secrets: map[string][]string{}, metadataOnly: metadataOnly, inputs: map[string]bool{canonicalSource: true}}
	r.sourceGitPaths = sourceGitPaths
	for _, path := range sourceGitPaths {
		if !pathWithin(canonicalRun, path) {
			r.protectedGitPaths = append(r.protectedGitPaths, path)
		}
	}
	if selected, _ := ctx.Value(isolationCredentialsKey{}).(*isolationCredentials); selected != nil {
		r.credentials = selected
		selected.mu.Lock()
		for id, values := range selected.values {
			r.secrets[id] = append([]string(nil), values...)
		}
		selected.mu.Unlock()
	}
	if parent != nil {
		for source := range parent.inputs {
			r.inputs[source] = true
		}
		r.protectedGitPaths = append(r.protectedGitPaths, parent.protectedGitPaths...)
	}
	r.relay = r.opts.RelayExecutable
	if r.relay == "" && !metadataOnly {
		r.relay, err = os.Executable()
		if err != nil {
			cancel()
			return ctx, func() {}, err
		}
	}
	if r.relay != "" {
		r.relay, err = filepath.EvalSymlinks(r.relay)
	}
	if err != nil {
		cancel()
		return ctx, func() {}, errors.New("isolation relay executable could not be resolved")
	}
	cleanup := func() {
		cancel()
		for _, bridge := range r.bridges {
			_ = bridge.server.Close()
			_ = bridge.listener.Close()
			wait, done := context.WithTimeout(context.Background(), 10*time.Second)
			_ = bridge.bridge.Shutdown(wait)
			done()
		}
		r.mu.Lock()
		clear(r.secrets)
		r.mu.Unlock()
	}
	for _, candidate := range candidates {
		if candidate.Harness != "antigravity-bridge" {
			continue
		}
		bridge, err := r.startBridge(ctx, candidate)
		if err != nil {
			cleanup()
			return ctx, func() {}, err
		}
		r.bridges[candidate.ID] = bridge
		r.addSecrets(candidate.ID, bridge.key)
	}
	return context.WithValue(ctx, isolationContextKey{}, r), cleanup, nil
}

func (r *isolationRuntime) startBridge(ctx context.Context, candidate Candidate) (isolationBridge, error) {
	if !workflowID.MatchString(candidate.ID) {
		return isolationBridge{}, errors.New("isolated inference evidence requires a valid candidate ID")
	}
	dir, err := os.OpenRoot(r.run)
	if err != nil {
		return isolationBridge{}, err
	}
	for _, path := range []string{"inference", filepath.Join("inference", candidate.ID)} {
		err = dir.Mkdir(path, 0700)
		if errors.Is(err, os.ErrExist) {
			info, statErr := dir.Lstat(path)
			if statErr == nil && info.IsDir() && info.Mode().Perm()&0077 == 0 {
				err = nil
			}
		}
		if err != nil {
			break
		}
	}
	dir.Close()
	if err != nil {
		return isolationBridge{}, errors.New("private native inference evidence directory could not be admitted")
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return isolationBridge{}, err
	}
	key := hex.EncodeToString(random[:])
	timeout := 90 * time.Second
	if candidate.TimeoutSeconds > 0 && candidate.TimeoutSeconds <= 120 {
		timeout = time.Duration(candidate.TimeoutSeconds) * time.Second
	}
	env := map[string]string{}
	for _, name := range []string{"XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"} {
		if value := os.Getenv(name); value != "" {
			env[name] = value
		}
	}
	command := append([]string(nil), r.opts.BridgeCommand...)
	if len(command) == 0 {
		command = []string{"agy"}
	}
	selected, err := exec.LookPath(command[0])
	if err != nil {
		return isolationBridge{}, errors.New("isolated Antigravity bridge requires its selected host inference executable")
	}
	command[0], err = filepath.EvalSymlinks(selected)
	if err != nil {
		return isolationBridge{}, errors.New("host inference executable could not be resolved")
	}
	observe := func(evidence agybridge.Evidence) error {
		evidenceCtx := context.WithValue(ctx, isolationContextKey{}, r)
		changed, err := sanitizeIsolationEvidence(evidenceCtx, &evidence)
		if err != nil {
			return err
		}
		if changed {
			evidence.CredentialsRedacted = true
			digest := sha256.Sum256([]byte(evidence.Input))
			evidence.InputSHA256 = hex.EncodeToString(digest[:])
		}
		path := filepath.Join(r.run, "inference", candidate.ID, fmt.Sprintf("request-%06d.json", evidence.Index))
		return saveIsolationJSON(evidenceCtx, path, &evidence)
	}
	b, err := agybridge.New(agybridge.Options{Key: key, Models: []string{candidate.Model}, Command: command, Env: env, Timeout: timeout, MaxConcurrent: 1, MaxRequests: r.opts.BridgeMaxRequests, Wrap: sandbox.WrapProcessTree, ReapDescendants: true, Observe: observe})
	if err != nil {
		return isolationBridge{}, err
	}
	l, err := agybridge.Listen("127.0.0.1:0")
	if err != nil {
		return isolationBridge{}, err
	}
	server := &http.Server{Handler: b, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 2 * time.Minute, WriteTimeout: 2 * time.Minute, MaxHeaderBytes: 64 << 10}
	go func() { _ = server.Serve(l) }()
	go func() { <-ctx.Done(); _ = server.Close() }()
	return isolationBridge{base: "http://" + l.Addr().String() + "/v1", key: key, listener: l, server: server, bridge: b}, nil
}

func (r *isolationRuntime) addSecrets(id string, values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.secrets[id] = append(r.secrets[id], values...)
	if r.credentials != nil {
		r.credentials.mu.Lock()
		r.credentials.values[id] = append(r.credentials.values[id], values...)
		r.credentials.mu.Unlock()
	}
}

func (r *isolationRuntime) wrapper(repo string, readOnly bool, paths []string, homes []sandbox.HomeFile, socket, certificate string) func(harness.Spec) (harness.Spec, error) {
	return func(spec harness.Spec) (harness.Spec, error) {
		dependencies := append(append(append([]string(nil), r.opts.ReadOnlyPaths...), paths...), spec.ReadOnlyPaths...)
		if err := r.validateNativeGitDependencies(dependencies); err != nil {
			return harness.Spec{}, err
		}
		network := "none"
		if socket != "" {
			network = "allowlist"
		}
		return sandbox.WrapWorkflow(spec, sandbox.WorkflowOptions{Workspace: repo, StateDir: r.run, ProtectedSourceGitPaths: r.protectedGitPaths, ProtectedSourcePaths: r.nativeSourcePaths(), ReadOnlyWorkspace: readOnly, ReadOnlyPaths: append(append([]string(nil), r.opts.ReadOnlyPaths...), paths...), HomeFiles: homes, Network: network, EgressSocket: socket, EgressCA: certificate, RelayExecutable: func() string {
			if socket != "" {
				return r.relay
			}
			return ""
		}(), HomeBytes: r.opts.HomeBytes, TempBytes: r.opts.TempBytes})
	}
}

// IsolationRequest attaches selected credentials and a per-turn exact-origin
// proxy. The cleanup revokes the proxy and deletes filtered login copies.
func IsolationRequest(ctx context.Context, candidate Candidate, req harness.Request, readOnly bool) (harness.Request, func(), error) {
	r := isolationFromContext(ctx)
	if r == nil {
		return req, func() {}, nil
	}
	cleanups := []func(){}
	cleanup := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}
	// Explicit Env is an owner-selected capability too. Register recognizable
	// credential names without inspecting unrelated ambient environment values.
	for name, value := range req.Env {
		if isolationCredentialEnvironment(name) && value != "" {
			r.addSecrets(candidate.ID, value)
		}
	}
	allow := append([]egress.Endpoint(nil), r.opts.Allow...)
	if candidate.Harness == "antigravity-bridge" {
		bridge, ok := r.bridges[candidate.ID]
		if !ok {
			return req, cleanup, errors.New("isolated Antigravity inference bridge was not prepared")
		}
		req.Harness, req.BaseURL, req.APIKeyEnv = "pi", bridge.base, "AGENTMUX_ISOLATED_BRIDGE_KEY"
		req.Env = cloneIsolationEnv(req.Env)
		req.Env[req.APIKeyEnv] = bridge.key
		origin := strings.TrimSuffix(bridge.base, "/v1")
		allow = append(allow, egress.Endpoint{Origin: origin, IPs: []string{"127.0.0.1"}})
	}
	paths := []string{}
	if req.Harness != "command" {
		binary := ""
		if len(req.Command) > 0 {
			binary = req.Command[0]
		}
		command, dependencies, err := managedsetup.Discover(req.Harness, binary)
		if err != nil {
			return req, cleanup, fmt.Errorf("isolated native code discovery: %w", err)
		}
		if len(req.Command) > 0 {
			command = append(command, req.Command[1:]...)
		}
		req.Command = command
		paths = append(paths, dependencies...)
	}
	for _, dependency := range append(append([]string(nil), r.opts.ReadOnlyPaths...), paths...) {
		canonical, err := filepath.EvalSymlinks(dependency)
		if err != nil {
			return req, cleanup, fmt.Errorf("isolated dependency is unavailable: %w", err)
		}
		for _, auth := range r.opts.Auth {
			if pathWithin(canonical, auth.File) {
				return req, cleanup, errors.New("isolated code dependency would expose an original authentication file")
			}
		}
	}
	homes := []sandbox.HomeFile{}
	if auth, ok := r.opts.Auth[candidate.ID]; ok {
		prepared, err := agentauth.PrepareAnonymous(req.Harness, managedconfig.NativeAuth{File: auth.File, Provider: auth.Provider})
		if err != nil {
			return req, cleanup, fmt.Errorf("isolated native authentication: %w", err)
		}
		if err := validatePinnedIsolationAuth(ctx, candidate.ID, prepared); err != nil {
			prepared.Close()
			return req, cleanup, err
		}
		cleanups = append(cleanups, prepared.Close)
		for _, file := range prepared.Files {
			homes = append(homes, sandbox.HomeFile{Source: file.Source, Target: file.Target, Descriptor: file.Descriptor})
		}
		r.addSecrets(candidate.ID, prepared.Secrets...)
	}
	if req.APIKeyEnv != "" {
		key := req.Env[req.APIKeyEnv]
		if key == "" {
			key = os.Getenv(req.APIKeyEnv)
		}
		if key != "" {
			r.addSecrets(candidate.ID, key)
		}
	}
	socket, certificate := "", ""
	if r.opts.Network == "allowlist" {
		dir, err := os.MkdirTemp("", "agentmux-isolated-egress-*")
		if err != nil {
			cleanup()
			return req, func() {}, err
		}
		cleanups = append(cleanups, func() { _ = os.RemoveAll(dir) })
		socket = filepath.Join(dir, "proxy.sock")
		server, publicCA, err := startIsolatedEgress(ctx, socket, allow)
		if err != nil {
			cleanup()
			return req, func() {}, fmt.Errorf("isolated egress policy could not be enforced: %w", err)
		}
		certificate = publicCA
		cleanups = append(cleanups, func() { _ = server.Close() })
	}
	req.Wrap = r.wrapper(req.Workdir, readOnly, paths, homes, socket, certificate)
	if req.Harness == "codex" {
		req.ExternalSandbox = true
	}
	return req, cleanup, nil
}

func isolationCredentialEnvironment(name string) bool {
	name = strings.ToUpper(name)
	for _, part := range []string{"API_KEY", "TOKEN", "SECRET", "PASSWORD", "CREDENTIAL"} {
		if strings.Contains(name, part) {
			return true
		}
	}
	return false
}

func cloneIsolationEnv(input map[string]string) map[string]string {
	result := make(map[string]string, len(input)+1)
	for key, value := range input {
		result[key] = value
	}
	return result
}

func IsolationCheckRequest(ctx context.Context, repo string, req harness.Request) (harness.Request, func(), error) {
	if r := isolationFromContext(ctx); r != nil {
		req.Wrap = r.wrapper(repo, false, nil, nil, "", "")
		req.Env = nil
	}
	return req, func() {}, nil
}

// WrapIsolatedGit restricts fixed controller Git plumbing to its selected
// private repository plus the original read-only source input. Model-authored
// Git commands already remain inside the native/check process namespace.
func WrapIsolatedGit(ctx context.Context, cmd *exec.Cmd) error {
	r := isolationFromContext(ctx)
	if r == nil {
		return nil
	}
	repo := ""
	for i := 1; i+1 < len(cmd.Args); i++ {
		if cmd.Args[i] == "-C" {
			repo = cmd.Args[i+1]
			break
		}
	}
	if repo == "" {
		return errors.New("isolated Git requires an explicit repository directory")
	}
	repo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		return err
	}
	// Source path selection is trusted controller bootstrap, like the bounded
	// Go raw-file reader. Preserve the owner's effective ignore policy here;
	// mounting a blank HOME would otherwise select globally ignored secrets.
	// Only this fixed read-only builtin is permitted outside the filesystem
	// namespace; no native/check/candidate command reaches this path.
	if repo == r.source && trustedSourceEnumeration(cmd, r.source) {
		spec, err := sandbox.WrapProcessTree(harness.Spec{Name: "command", Binary: cmd.Path, Args: append([]string(nil), cmd.Args[1:]...), Env: isolationGitEnvironment(cmd.Env), Workdir: cmd.Dir})
		if err != nil {
			return err
		}
		if len(cmd.ExtraFiles) > 1 {
			return errors.New("source enumeration supports one integration lease")
		}
		if len(cmd.ExtraFiles) == 1 {
			spec.Args = append([]string{"--sync-fd", "3"}, spec.Args...)
		}
		cmd.Path, cmd.Args, cmd.Env, cmd.Dir = spec.Binary, append([]string{spec.Binary}, spec.Args...), spec.Env, spec.Workdir
		return nil
	}
	sourceRepo := pathWithin(r.source, repo) && !pathWithin(r.run, repo)
	readOnly := sourceRepo
	writeSource := false
	if allowed, _ := ctx.Value(isolatedSourceWriteContextKey{}).(string); allowed == r.source && sourceRepo {
		operation, check := "", false
		for i := 1; i < len(cmd.Args); i++ {
			if cmd.Args[i] == "apply" {
				operation = "apply"
			}
			if cmd.Args[i] == "--check" {
				check = true
			}
		}
		if operation == "apply" && !check {
			readOnly = false
			writeSource = true
		}
	}
	if !readOnly && !writeSource && (!pathWithin(r.run, repo) || repo == r.run) {
		return errors.New("isolated Git repository lies outside selected private run state")
	}
	workspace, source := repo, ""
	if sourceRepo {
		workspace, source = r.source, r.source
	} else {
		for _, arg := range cmd.Args {
			if arg == r.source || strings.HasPrefix(arg, r.source+string(filepath.Separator)) || arg == "--work-tree="+r.source {
				source = r.source
				break
			}
		}
	}
	// Normal private repositories keep their Git database inside their own
	// worktree. read-tree -u and checkout must see that exact parent writable;
	// binding only .git would publish checkout files into ephemeral namespace
	// directories, silently leaving the host worktree empty.
	if !sourceRepo && filepath.Base(repo) == ".git" {
		for _, argument := range cmd.Args {
			if strings.HasPrefix(argument, "--work-tree=") {
				target := strings.TrimPrefix(argument, "--work-tree=")
				canonical, err := filepath.EvalSymlinks(target)
				if err != nil || canonical != target || canonical != filepath.Dir(repo) || !pathWithin(r.run, canonical) || canonical == r.run {
					return errors.New("isolated private Git working tree must be its exact private parent")
				}
				workspace = canonical
			}
		}
	}
	metadata := ""
	if sourceRepo {
		metadata = PrivateApplicationMetadata(ctx, r.source)
	}
	checkout := ""
	if quarantine, selected := PrivateGitQuarantine(ctx); quarantine != "" {
		if quarantine != repo || sourceRepo {
			return errors.New("controller quarantine capability does not match its exact Git process")
		}
		if selected != "" {
			canonical, err := filepath.EvalSymlinks(selected)
			if err != nil || canonical != selected || !pathWithin(r.run, selected) || selected == r.run {
				return errors.New("controller quarantine checkout must remain in one selected private worktree")
			}
			checkout = selected
		}
	}
	var sourceGitPaths []string
	if source != "" {
		for _, path := range r.sourceGitPaths {
			if !pathWithin(r.run, path) {
				sourceGitPaths = append(sourceGitPaths, path)
			}
		}
	}
	spec, err := sandbox.WrapWorkflow(harness.Spec{Name: "command", Binary: cmd.Path, Args: append([]string(nil), cmd.Args[1:]...), Env: isolationGitEnvironment(cmd.Env), Workdir: repo}, sandbox.WorkflowOptions{Workspace: workspace, StateDir: r.run, ControllerSource: source, ControllerSourceGitPaths: sourceGitPaths, ControllerWrite: writeSource, ControllerMetadata: metadata, ControllerWorkspace: checkout, ReadOnlyWorkspace: readOnly, ReadOnlyPaths: r.opts.ReadOnlyPaths, Network: "none", HomeBytes: r.opts.HomeBytes, TempBytes: r.opts.TempBytes})
	if err != nil {
		return err
	}
	if len(cmd.ExtraFiles) > 1 {
		return errors.New("isolated Git supports one explicit application lease")
	}
	if len(cmd.ExtraFiles) == 1 {
		spec.Args = append([]string{"--sync-fd", "3"}, spec.Args...)
	}
	cmd.Path, cmd.Args, cmd.Env, cmd.Dir = spec.Binary, append([]string{spec.Binary}, spec.Args...), spec.Env, spec.Workdir
	return nil
}

func isolationGitEnvironment(input []string) []string {
	var result []string
	for _, entry := range input {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "PATH", "HOME", "XDG_CONFIG_HOME", "LANG", "LC_ALL", "LC_CTYPE", "TMPDIR", "TMP", "TEMP":
			result = append(result, entry)
		case "GIT_TERMINAL_PROMPT", "GIT_CONFIG_NOSYSTEM", "GIT_NO_LAZY_FETCH", "GIT_ALLOW_PROTOCOL", "GIT_NO_REPLACE_OBJECTS":
			if privateGitDenyEnvironment(entry) {
				result = append(result, entry)
			}
		}
	}
	return result
}

func trustedSourceEnumeration(cmd *exec.Cmd, source string) bool {
	for _, marker := range []string{filepath.Join(source, ".git", privateStoreMarkerName), filepath.Join(source, privateStoreMarkerName)} {
		if _, err := os.Lstat(marker); err == nil {
			return false
		}
	}
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "GIT_") && !privateGitDenyEnvironment(entry) {
			return false
		}
	}
	args := cmd.Args[1:]
	seenC, hook, monitor := false, false, false
	for len(args) > 0 {
		if args[0] == "ls-files" {
			args = args[1:]
			break
		}
		if len(args) < 2 {
			return false
		}
		switch args[0] {
		case "-C":
			if seenC || args[1] != source {
				return false
			}
			seenC = true
		case "-c":
			switch args[1] {
			case "core.hooksPath=/dev/null":
				hook = true
			case "core.fsmonitor=false":
				monitor = true
			default:
				return false
			}
		default:
			return false
		}
		args = args[2:]
	}
	if !seenC || !hook || !monitor || len(args) == 0 {
		return false
	}
	for _, arg := range args {
		switch arg {
		case "--cached", "--others", "--exclude-standard", "-z", "--stage":
		default:
			return false
		}
	}
	return true
}

// RedactIsolationResult removes all exact selected run credential values before task
// evidence is retained. It does not claim to prevent intentional encodings or
// hide an agent's selected provider capability from that same agent.
func RedactIsolationResult(ctx context.Context, id string, result harness.Result) (harness.Result, bool) {
	clean, changed, err := redactIsolationNativeResult(ctx, result)
	if err != nil {
		// The compatibility API cannot report a refusal. Never return the unsafe
		// frames; workflow execution uses the error-returning boundary below.
		return harness.Result{}, true
	}
	return clean, changed
}

func redactIsolationNativeResult(ctx context.Context, result harness.Result) (harness.Result, bool, error) {
	values := selectedIsolationCredentials(ctx)
	if len(values) == 0 {
		return result, false, nil
	}
	changed := false
	redact := func(text string) string {
		clean := redactIsolationText(values, text)
		changed = changed || clean != text
		return clean
	}
	result.Text = redact(result.Text)
	result.SessionID = redact(result.SessionID)
	for i, frame := range result.Events {
		clean, altered, err := redactIsolationJSON(ctx, frame)
		if err != nil {
			return harness.Result{}, true, err
		}
		result.Events[i] = clean
		changed = changed || altered
	}
	if len(result.Usage) > 0 {
		clean, altered, err := redactIsolationJSON(ctx, result.Usage)
		if err != nil {
			return harness.Result{}, true, err
		}
		result.Usage = clean
		changed = changed || altered
	}
	return result, changed, nil
}

func (r *isolationRuntime) secretValues(id string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id != "" {
		return append([]string(nil), r.secrets[id]...)
	}
	var values []string
	for _, selected := range r.secrets {
		values = append(values, selected...)
	}
	return values
}

type redactedIsolationError struct {
	original error
	message  string
}

func (e *redactedIsolationError) Error() string { return e.message }
func (e *redactedIsolationError) Unwrap() error { return e.original }

// Preserve causal error identity for cancellation and exit handling while
// ensuring selected credential values never enter retained error diagnostics.
func RedactIsolationError(ctx context.Context, id string, err error) error {
	if err == nil {
		return nil
	}
	return redactEarlyIsolationError(ctx, err)
}

func redactJSONStrings(data []byte, redact func(string) string) []byte {
	var out bytes.Buffer
	for i := 0; i < len(data); {
		if data[i] != '"' {
			out.WriteByte(data[i])
			i++
			continue
		}
		start := i
		i++
		for i < len(data) {
			if data[i] == '\\' {
				i += 2
				continue
			}
			if data[i] == '"' {
				i++
				break
			}
			i++
		}
		if i > len(data) {
			return data
		}
		var text string
		if json.Unmarshal(data[start:i], &text) != nil {
			return data
		}
		value := redact(text)
		if value == text {
			out.Write(data[start:i])
		} else {
			encoded, _ := json.Marshal(value)
			out.Write(encoded)
		}
	}
	return out.Bytes()
}
