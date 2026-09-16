// Package protocol defines the version-agnostic transport contract that
// higher layers (pkg/device, pkg/discovery) depend on. Concrete
// protocol versions (pkg/protocol35, and future pkg/protocol3x
// packages) implement Session without the rest of the codebase knowing
// which version is in use.
package protocol
