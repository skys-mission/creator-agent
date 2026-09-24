package contract

import "sync"

// SandboxController holds a per-session override for OS-level sandbox isolation, toggled at runtime
// via the /sandbox command. A nil override follows the base policy; a non-nil override forces
// isolation on or off for the rest of the session. Safe for concurrent use.
//
// This mirrors the ModeController pattern on purpose: a runtime toggle must take effect without
// rebuilding the agent.
type SandboxController struct {
	mu       sync.RWMutex
	override *bool
}

// NewSandboxController returns a controller with no override (follow base policy).
func NewSandboxController() *SandboxController { return &SandboxController{} }

// Override returns the current override (nil = follow base policy).
func (c *SandboxController) Override() *bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.override == nil {
		return nil
	}
	v := *c.override
	return &v
}

// Set updates the override. Pass nil to clear it (return to base policy).
func (c *SandboxController) Set(v *bool) {
	c.mu.Lock()
	if v == nil {
		c.override = nil
	} else {
		b := *v
		c.override = &b
	}
	c.mu.Unlock()
}
