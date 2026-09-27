package engine

// resetRegistry clears detector registration state between tests so tests
// can register their own fixtures without colliding with other tests or
// with detectors registered by internal/detect packages.
func resetRegistry() {
	registry = nil
	registryIdx = map[string]bool{}
}
