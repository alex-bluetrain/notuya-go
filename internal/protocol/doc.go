// Package protocol defines the version-agnostic transport contract that
// higher layers (internal/device, internal/discovery) depend on. Concrete
// protocol versions (internal/protocol35, and future internal/protocol3x
// packages) implement Session without the rest of the codebase knowing
// which version is in use.
package protocol
