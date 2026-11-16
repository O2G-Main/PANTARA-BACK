package project

import "time"

// Project groups related tasks for a client or internal initiative.
type Project struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organizationId,omitempty"`
	Name           string    `json:"name"`
	Client         string    `json:"client"`
	DueDate        string    `json:"dueDate"`
	IsArchived     bool      `json:"isArchived,omitempty"`
	CreatedAt      time.Time `json:"createdAt,omitempty"`
	UpdatedAt      time.Time `json:"updatedAt,omitempty"`
}
