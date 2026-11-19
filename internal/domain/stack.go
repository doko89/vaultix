package domain

import (
	"fmt"
	"strings"
	"time"
)

// ApplicationStack groups related resources on one VM into a logical backup
// unit (PRD §7.8). MVP keeps this minimal: membership + restore order.
// Full automated stack recovery is post-MVP.
type ApplicationStack struct {
	ID          string
	AgentID     string
	Name        string
	Description string
	// ComposePath points at the compose file when the agent can access it.
	ComposePath string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Validate checks stack invariants.
func (s ApplicationStack) Validate() error {
	if strings.TrimSpace(s.AgentID) == "" {
		return fmt.Errorf("%w: agent_id is required", ErrValidation)
	}
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("%w: stack name is required", ErrValidation)
	}
	return nil
}

// ApplicationStackResource is one member of a stack (PRD
// `application_stack_resources`). RestoreOrder defines the explicit restore
// sequence (lower first).
type ApplicationStackResource struct {
	StackID string
	// ResourceID references a DiscoveredResource; Selector pins the exact
	// backup selector when the resource no longer exists.
	ResourceID   string
	Selector     string
	Type         TargetType
	RestoreOrder int
	// Required marks members whose backup must succeed for the stack
	// backup to count as complete.
	Required bool
}
