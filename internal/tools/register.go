package tools

// Register installs every built-in tool into a registry.
func Register(r *Registry) {
	registerFiles(r)
	registerShell(r)
	registerWeb(r)
	registerBrowser(r)
	registerAgentOps(r)
	registerSecurity(r)
	registerSecTool(r)
	registerRecon(r)
	registerReverse(r)
	registerCodec(r)
	registerVPS(r)
}

// Default returns a registry with all built-ins installed.
func Default() *Registry {
	r := NewRegistry()
	Register(r)
	return r
}
