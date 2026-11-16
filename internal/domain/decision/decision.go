package decision

import "github.com/Orang2-BU/pantara-back/internal/domain/workspace"

// OverrideReasonLabels maps valid override reason codes to human Indonesian labels.
var OverrideReasonLabels = map[string]string{
	"klien":        "Konteks klien",
	"pengembangan": "Pengembangan karyawan",
	"info":         "Ada info di luar sistem",
	"lainnya":      "Alasan lain",
}

// RejectReasonLabels maps valid rejection reason codes to human Indonesian labels.
var RejectReasonLabels = map[string]string{
	"tidak-sesuai": "Semua kandidat tidak sesuai",
	"kebutuhan":    "Analisis kebutuhan keliru",
	"tunda":        "Tugas ditunda dulu",
	"lainnya":      "Alasan lain",
}

// Decision is an append-only audit entry capturing who decided, why, and how long it took.
type Decision struct {
	ID             string             `json:"id"`
	OrganizationID string             `json:"organizationId,omitempty"`
	TaskID         string             `json:"taskId"`
	DecidedBy      string             `json:"decidedBy,omitempty"`
	MemberIDs      []string           `json:"memberIds"`
	Rank           *int               `json:"rank"`
	Rejected       bool               `json:"rejected,omitempty"`
	Preset         workspace.PresetID `json:"preset"`
	ReasonCode     string             `json:"reasonCode,omitempty"`
	Note           string             `json:"note,omitempty"`
	DecidedAt      string             `json:"decidedAt"`
	DecisionMs     int64              `json:"decisionMs"`
	ConflictsSeen  int                `json:"conflictsSeen"`
}
