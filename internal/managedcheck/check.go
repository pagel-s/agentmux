// Package managedcheck performs bounded, non-generative launch checks without
// initializing managed state or changing native provider configuration.
package managedcheck

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agentmux/internal/agentauth"
	"agentmux/internal/harness"
	"agentmux/internal/managedconfig"
	"agentmux/internal/sandbox"
)

const (
	StatusOK      = "ok"
	StatusWarning = "warning"
	StatusError   = "error"
	probeTimeout  = 3 * time.Second
)

type Options struct {
	Executable    string
	StateDir      string
	BrokerSandbox string
	CheckAuth     bool
}

// CheckResult deliberately contains no raw command output, credentials, or
// provider response bodies. Native versions and capability names are safe
// metadata; authentication results are reduced to readiness only.
type CheckResult struct {
	Agent   string `json:"agent,omitempty"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type Report struct {
	OK     bool          `json:"ok"`
	Checks []CheckResult `json:"checks"`
}

type checker struct {
	ctx         context.Context
	cfg         managedconfig.Config
	opts        Options
	report      Report
	initialized bool
	executable  string
	metadata    map[string]metadataResult
}

// Check never makes a model request. Help/version processes have three-second
// deadlines and private temporary homes; optional auth checks use only local
// readiness metadata. Endpoint discovery is limited to configured loopback or
// private destinations and never follows redirects. Missing credentials and
// unavailable requested isolation are errors; unchecked native auth and public
// endpoints are honest warnings. Only errors make Report.OK false.
//
// Callers resolve configured Workdir and ReadOnlyPaths against their config
// location before calling. No managed directory or workspace is created here.
func Check(ctx context.Context, cfg managedconfig.Config, opts Options) Report {
	c := checker{ctx: ctx, cfg: cfg.Normalized(), opts: opts, report: Report{Checks: []CheckResult{}}, metadata: map[string]metadataResult{}}
	if err := c.cfg.Validate(); err != nil {
		c.add("", "config", StatusError, err.Error())
		return c.finish()
	}
	c.add("", "config", StatusOK, "managed configuration is valid")
	if err := agentauth.CheckBoundary(c.cfg); err != nil {
		c.add("", "auth.boundary", StatusError, err.Error())
		return c.finish()
	}
	if c.opts.BrokerSandbox == "" {
		c.opts.BrokerSandbox = "local"
	}
	if c.opts.Executable == "" {
		c.opts.Executable, _ = os.Executable()
	}
	var err error
	c.executable, err = executablePath(c.opts.Executable, "", nil)
	if err != nil {
		c.add("", "agentmux.binary", StatusError, "Agentmux executable is unavailable or not executable")
	} else {
		c.add("", "agentmux.binary", StatusOK, "Agentmux executable is available")
	}
	if c.opts.StateDir != "" {
		if !filepath.IsAbs(c.opts.StateDir) {
			c.add("", "state.path", StatusError, "state path must be resolved to an absolute path")
		} else {
			c.initialized = regularFile(filepath.Join(c.opts.StateDir, "state.sqlite"))
			if !c.initialized {
				c.add("", "state.path", StatusWarning, "state is not initialized; no files were created")
			}
		}
	}
	needsBubblewrap := c.opts.BrokerSandbox == "bubblewrap"
	for _, agent := range c.cfg.Agents {
		needsBubblewrap = needsBubblewrap || agent.Sandbox == "bubblewrap"
	}
	if c.cfg.Spawn.Enabled {
		for _, agent := range c.cfg.Spawn.Templates {
			needsBubblewrap = needsBubblewrap || agent.Sandbox == "bubblewrap"
		}
	}
	if needsBubblewrap {
		probeCtx, cancel := context.WithTimeout(c.ctx, probeTimeout)
		err := sandbox.Probe(probeCtx)
		cancel()
		if err != nil {
			c.add("", "sandbox.bubblewrap", StatusError, "bubblewrap namespaces are unavailable; requested isolation cannot start")
		} else {
			c.add("", "sandbox.bubblewrap", StatusOK, "a real bubblewrap namespace probe succeeded")
		}
	}
	switch c.opts.BrokerSandbox {
	case "local":
		c.add("", "broker.sandbox", StatusWarning, "broker runs as a trusted host process; select bubblewrap for isolation")
	case "bubblewrap":
		if c.initialized && c.executable != "" {
			if _, err := sandbox.WrapBroker(c.executable, c.opts.StateDir); err != nil {
				c.add("", "broker.sandbox", StatusError, "broker sandbox paths or private state permissions are invalid")
			} else {
				c.add("", "broker.sandbox", StatusOK, "broker sandbox mounts and private state permissions are valid")
			}
		} else {
			c.add("", "broker.sandbox", StatusWarning, "broker isolation requested; exact mounts will be validated after state initialization")
		}
	default:
		c.add("", "broker.sandbox", StatusError, "unsupported broker sandbox")
	}
	ids := make([]string, 0, len(c.cfg.Agents))
	for id := range c.cfg.Agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if c.ctx.Err() != nil {
			c.add(id, "preflight", StatusError, "preflight was cancelled or its deadline expired")
			continue
		}
		c.agent(id, c.cfg.Agents[id])
	}
	if c.cfg.Spawn.Enabled {
		templates := make([]string, 0, len(c.cfg.Spawn.Templates))
		for id := range c.cfg.Spawn.Templates {
			templates = append(templates, id)
		}
		sort.Strings(templates)
		for _, id := range templates {
			c.agent("template:"+id, c.cfg.Spawn.Templates[id])
		}
	}
	return c.finish()
}

func (c *checker) add(agent, name, status, message string) {
	c.report.Checks = append(c.report.Checks, CheckResult{Agent: agent, Name: name, Status: status, Message: message})
}

func (c *checker) finish() Report {
	sort.SliceStable(c.report.Checks, func(i, j int) bool {
		a, b := c.report.Checks[i], c.report.Checks[j]
		if a.Agent != b.Agent {
			return a.Agent < b.Agent
		}
		return a.Name < b.Name
	})
	c.report.OK = true
	for _, check := range c.report.Checks {
		if check.Status == StatusError {
			c.report.OK = false
		}
	}
	return c.report
}

func (c *checker) agent(id string, agent managedconfig.Agent) {
	resources := sandbox.Limits{MemoryBytes: agent.Resources.MemoryBytes, CPUPercent: agent.Resources.CPUPercent, Processes: agent.Resources.Processes, OpenFiles: agent.Resources.OpenFiles, CPUSeconds: agent.Resources.CPUSeconds, AddressSpaceBytes: agent.Resources.AddressSpaceBytes}
	if resources.Enabled() {
		probeCtx, stop := context.WithTimeout(c.ctx, probeTimeout)
		err := sandbox.ProbeLimits(probeCtx, resources)
		stop()
		if err != nil {
			c.add(id, "resources", StatusError, "requested kernel resource limits could not be enforced; no unlimited fallback is permitted")
		} else {
			c.add(id, "resources", StatusOK, "actual kernel resource controls were verified without a model request")
		}
	}
	if agent.Sandbox == "bubblewrap" {
		for key := range agent.Env {
			if strings.HasPrefix(key, "LD_") || key == "GCONV_PATH" || key == "LOCPATH" || key == "GLIBC_TUNABLES" {
				c.add(id, "sandbox.environment", StatusError, "agent environment contains a variable unsafe for the namespace launcher")
				return
			}
		}
		if agent.Network == "none" && agent.Harness != "command" {
			c.add(id, "connection.network", StatusError, "native model harness needs provider network access; network none cannot reach HTTP model endpoints")
		}
	}
	workdir := agent.Workdir
	if workdir == "" && c.opts.StateDir != "" {
		workdir = filepath.Join(c.opts.StateDir, "workspaces", id)
	}
	workspaceOK := c.workspace(id, workdir, agent.Sandbox)
	pathsOK := c.dependencies(id, agent.ReadOnlyPaths, workdir, agent.Sandbox)
	connection := c.cfg.Connections[agent.Connection]
	key, keySet := "", false
	if connection.APIKeyEnv != "" {
		key, keySet = agent.Env[connection.APIKeyEnv]
		if !keySet {
			key, keySet = os.LookupEnv(connection.APIKeyEnv)
		}
		if !keySet || key == "" {
			c.add(id, "credential.env", StatusError, "configured credential environment variable is missing or empty")
		} else {
			c.add(id, "credential.env", StatusOK, "configured credential environment variable is present; value is not displayed")
		}
	}
	if agent.Harness == "pi" && connection.BaseURL != "" {
		discovery := DiscoverModelMetadata(c.ctx, connection, agent.Model, key)
		metadata := discovery.Metadata.Effective()
		message := fmt.Sprintf("%s: context=%d output=%d; capability metadata is advisory, not a transport guarantee", discovery.Source, metadata.ContextWindow, metadata.MaxOutput)
		status := StatusOK
		if discovery.Warning != "" {
			status = StatusWarning
			message = discovery.Warning + "; " + message
		}
		if metadata.ContextWindow <= 4096 {
			status = StatusError
		}
		c.add(id, "model.metadata", status, message)
	}
	req := harness.Request{Harness: agent.Harness, Model: agent.Model, Workdir: workdir, BaseURL: connection.BaseURL, APIKeyEnv: connection.APIKeyEnv, ModelMetadata: connection.ModelMetadata, Command: append([]string(nil), agent.Command...), Env: agent.Env}
	if agent.CodexSandbox == "external" {
		req.ExternalSandbox = true
		// Check renders a specification; it never starts a model turn. Keep
		// even accidental future execution of this metadata request fenced.
		req.Wrap = func(harness.Spec) (harness.Spec, error) {
			return harness.Spec{}, errors.New("metadata checks cannot execute external model turns")
		}
		c.add(id, "codex.sandbox", StatusWarning, "explicit external mode: Codex commands rely on the required outer bubblewrap boundary; no native sandbox fallback is attempted")
	}
	spec, err := harness.Build(req)
	if err != nil {
		// Missing named credentials have their own useful, non-secret result.
		c.add(id, "harness.config", StatusError, "harness request cannot be built; check supported connection, model, environment, and argv configuration")
		return
	}
	binary, err := executablePath(spec.Binary, workdir, spec.Env)
	if err != nil {
		c.add(id, "harness.binary", StatusError, "configured harness executable is unavailable or not executable")
		return
	}
	spec.Binary = binary
	c.add(id, "harness.binary", StatusOK, "configured harness executable is available")
	if agent.Harness == "codex" {
		c.codexRuntime(id, agent, binary, workdir, spec.Env)
	}
	if agent.Sandbox == "bubblewrap" {
		c.scriptClosure(id, agent, binary, workdir, spec.Env)
	}
	meta := metadataResult{}
	if agent.Harness == "command" {
		c.add(id, "harness.capabilities", StatusWarning, "custom command was not executed by preflight; its stdin/exit contract is owner-defined")
	} else {
		cacheKey := agent.Harness + "\x00" + binary + "\x00" + strings.Join(specPrefix(agent), "\x00") + "\x00" + strings.Join(metadataEnv(agent.Env), "\x00")
		var exists bool
		meta, exists = c.metadata[cacheKey]
		if !exists {
			meta = inspectMetadata(c.ctx, agent, binary)
			c.metadata[cacheKey] = meta
		}
		if !meta.helpOK {
			c.add(id, "harness.capabilities", StatusError, "required CLI help could not be read within the bounded metadata check")
		} else if len(meta.missing) > 0 {
			c.add(id, "harness.capabilities", StatusError, "installed CLI lacks required adapter flags: "+strings.Join(meta.missing, ", "))
		} else {
			c.add(id, "harness.capabilities", StatusOK, "installed CLI advertises the required managed-turn flags")
		}
		if meta.version != "" {
			c.add(id, "harness.version", StatusOK, "installed CLI version "+meta.version)
		} else {
			c.add(id, "harness.version", StatusWarning, "CLI version could not be identified from its bounded version check")
		}
	}
	if agent.Sandbox == "local" {
		c.add(id, "sandbox", StatusWarning, "trusted host process; filesystem, network, and peer credentials are not isolated")
	} else if c.initialized && workspaceOK && pathsOK && c.executable != "" {
		credential := filepath.Join(c.opts.StateDir, "credentials", id+".json")
		socket := filepath.Join(c.opts.StateDir, "broker.sock")
		if regularFile(credential) && isSocket(socket) {
			deps := append(append([]string(nil), agent.ReadOnlyPaths...), c.executable)
			probeNetwork := agent.Network
			if probeNetwork == "allowlist" {
				probeNetwork = "none"
			}
			wrapped, err := sandbox.Wrap(spec, sandbox.Options{Mode: "bubblewrap", Network: probeNetwork, Workspace: workdir, StateDir: c.opts.StateDir, CredentialFile: credential, SocketPath: socket, ReadOnlyPaths: deps})
			if err != nil {
				c.add(id, "sandbox", StatusError, "sandbox mounts are invalid; check workspace, read-only dependencies, credential, and socket paths")
			} else if agent.Harness != "command" && meta.helpOK {
				// Rebuild only the help argv, retaining the already-validated
				// mount/environment boundary. No prompt or model call is sent.
				helpSpec := spec
				helpSpec.Args = metadataArgs(agent, "help")
				wrapped, err = sandbox.Wrap(helpSpec, sandbox.Options{Mode: "bubblewrap", Network: "none", Workspace: workdir, StateDir: c.opts.StateDir, CredentialFile: credential, SocketPath: socket, ReadOnlyPaths: deps})
				if err == nil {
					_, err = boundedCommand(c.ctx, wrapped, false)
				}
				if err != nil {
					c.add(id, "sandbox", StatusError, "CLI help could not run inside the requested filesystem boundary; declare required runtime/package read_only_paths")
				} else {
					c.add(id, "sandbox", StatusOK, "CLI help ran inside the declared sandbox without a model request")
				}
			} else {
				c.add(id, "sandbox", StatusOK, "declared sandbox mounts are valid; custom command was not executed")
			}
		} else {
			c.add(id, "sandbox", StatusWarning, "exact sandbox preflight deferred until this agent's credential and broker socket exist")
		}
	} else {
		c.add(id, "sandbox", StatusWarning, "isolation requested; exact mounts are deferred until state and workspace exist")
	}
	if agent.Network == "allowlist" {
		c.add(id, "network.allowlist", StatusWarning, "exact-origin rules passed config validation; help uses network:none, while per-turn proxy DNS/socket enforcement is checked by the worker before execution")
	}
	if connection.BaseURL != "" {
		status, message := probeEndpoint(c.ctx, connection, agent.Model, key)
		c.add(id, "connection", status, message)
	} else if agent.Harness != "command" {
		c.auth(id, agent, binary, connection.APIKeyEnv != "" && keySet && key != "")
	}
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func isSocket(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

func (c *checker) auth(id string, agent managedconfig.Agent, binary string, namedKey bool) {
	if agent.Auth.File != "" {
		c.fileAuth(id, agent, binary)
		return
	}
	if namedKey {
		c.add(id, "auth", StatusWarning, "named API credential is configured; provider acceptance was not tested")
		return
	}
	if !c.opts.CheckAuth {
		c.add(id, "auth", StatusWarning, "native authentication is unverified; optional local auth metadata checking was not requested")
		return
	}
	ready, verified := inspectAuth(c.ctx, agent, binary)
	if !verified {
		c.add(id, "auth", StatusWarning, "native authentication could not be verified using supported local metadata")
	} else if !ready {
		c.add(id, "auth", StatusError, "native CLI reports no ready local credential; sign in separately or configure a named API key")
	} else if agent.Sandbox == "bubblewrap" {
		c.add(id, "auth", StatusWarning, "host login metadata is present; access inside the private sandbox home remains unverified")
	} else {
		c.add(id, "auth", StatusOK, "native CLI reports ready local credential metadata; no provider request was made")
	}
}

func (c *checker) fileAuth(id string, agent managedconfig.Agent, binary string) {
	prepared, err := agentauth.Prepare(agent.Harness, agent.Auth)
	if err != nil {
		c.add(id, "auth", StatusError, err.Error())
		return
	}
	defer prepared.Close()
	if !c.opts.CheckAuth || !c.initialized || !regularFile(filepath.Join(c.opts.StateDir, "credentials", id+".json")) || !isSocket(filepath.Join(c.opts.StateDir, "broker.sock")) {
		c.add(id, "auth", StatusWarning, "selected private credential file is valid; native readiness inside the sandbox will be checked when initialized with a broker socket")
		return
	}
	args, supported := authArgs(agent)
	if !supported {
		c.add(id, "auth", StatusWarning, "selected credential file is valid; installed harness has no supported bounded native readiness probe")
		return
	}
	workdir := agent.Workdir
	if workdir == "" {
		workdir = filepath.Join(c.opts.StateDir, "workspaces", id)
	}
	build, err := harness.Build(harness.Request{Harness: "command", Command: append([]string{binary}, args...), Env: agent.Env, Workdir: workdir})
	var files []sandbox.HomeFile
	for _, file := range prepared.Files {
		files = append(files, sandbox.HomeFile{Source: file.Source, Target: file.Target})
	}
	if err == nil {
		build, err = sandbox.Wrap(build, sandbox.Options{Mode: "bubblewrap", Network: "none", Workspace: workdir, StateDir: c.opts.StateDir, CredentialFile: filepath.Join(c.opts.StateDir, "credentials", id+".json"), SocketPath: filepath.Join(c.opts.StateDir, "broker.sock"), ReadOnlyPaths: agent.ReadOnlyPaths, HomeFiles: files})
	}
	if err != nil {
		c.add(id, "auth", StatusError, "native auth sandbox mounts could not be prepared")
		return
	}
	output, probeErr := boundedCommand(c.ctx, build, false)
	ready, verified := authReadiness(agent.Harness, output, probeErr)
	if !verified {
		c.add(id, "auth", StatusError, "native readiness could not be verified inside the configured credential boundary")
	} else if !ready {
		c.add(id, "auth", StatusError, "native CLI reports no ready credential inside its private sandbox home")
	} else {
		c.add(id, "auth", StatusOK, "native CLI confirms ready credential metadata inside its private sandbox; network disabled and no provider request made")
	}
}
