package runtime

// NewControllerForTest exposes the controller constructor to the external test package.
func NewControllerForTest(m *Managed) *Controller { return newController(m) }
