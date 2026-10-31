package managedconfig

func supportedAction(action string) bool {
	switch action {
	case "message.send", "ping.send", "instruct":
		return true
	default:
		return false
	}
}

// Can authorizes only implemented agent-to-agent operations. A wildcard grant
// never authorizes an unknown agent or an unsupported future operation.
// Authentication and owner authority belong to the runtime, not these names.
func (c Config) Can(actor, action, target string) bool {
	if !c.knownAgent(actor) || !c.knownAgent(target) || !supportedAction(action) {
		return false
	}
	if c.Policy.Default != "" && c.Policy.Default != "deny" {
		return false
	}
	for _, grant := range c.Policy.Grants {
		if matches(grant.From, actor) && matches(grant.To, target) && matches(grant.Actions, action) {
			return true
		}
	}
	return false
}

func (c Config) CanReadBoard(actor, board string) bool {
	access, exists := c.Boards[board]
	return exists && c.knownAgent(actor) && matches(access.Readers, actor)
}

func (c Config) CanWriteBoard(actor, board string) bool {
	access, exists := c.Boards[board]
	return exists && c.knownAgent(actor) && matches(access.Writers, actor)
}

// CanReadMemory and CanWriteMemory address exactly configured shared
// namespaces. Private per-agent namespaces are handled by the broker.
func (c Config) CanReadMemory(actor, namespace string) bool {
	access, exists := c.Memory[namespace]
	return exists && c.knownAgent(actor) && matches(access.Readers, actor)
}

func (c Config) CanWriteMemory(actor, namespace string) bool {
	access, exists := c.Memory[namespace]
	return exists && c.knownAgent(actor) && matches(access.Writers, actor)
}

func (c Config) knownAgent(id string) bool {
	if !idPattern.MatchString(id) {
		return false
	}
	_, exists := c.Agents[id]
	return exists
}

func matches(selectors []string, value string) bool {
	for _, selector := range selectors {
		if selector == value || selector == "*" {
			return true
		}
	}
	return false
}
