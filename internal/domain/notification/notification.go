package notification

import "time"

// Kind classifies a persisted or operational notification.
type Kind string

const (
	KindTaskAssigned Kind = "task_assigned"
	KindTaskResponse Kind = "task_response"
	KindReminder     Kind = "reminder"
	KindOperational  Kind = "operational"
)

// Notice represents an inbox notification delivered to a user.
type Notice struct {
	ID             string     `json:"id"`
	OrganizationID string     `json:"organizationId,omitempty"`
	UserID         string     `json:"userId,omitempty"`
	Title          string     `json:"title"`
	Subtitle       string     `json:"subtitle"`
	Href           string     `json:"href"`
	Kind           Kind       `json:"kind"`
	Read           bool       `json:"read"`
	ReadAt         *time.Time `json:"readAt,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
}
