package exchange

import "time"

const (
	ExchangeProtocolV1 = "procoder-exchange/v1"
	ReturnProtocolV1   = "procoder-return/v1"
)

type Exchange struct {
	Protocol    string          `json:"protocol"`
	ExchangeID  string          `json:"exchange_id"`
	CreatedAt   time.Time       `json:"created_at"`
	ToolVersion string          `json:"tool_version"`
	Source      ExchangeSource  `json:"source"`
	Task        ExchangeTask    `json:"task"`
	Context     ExchangeContext `json:"context"`
}

type ExchangeSource struct {
	HeadRef string `json:"head_ref"`
	HeadOID string `json:"head_oid"`
}

type ExchangeTask struct {
	RootRef   string `json:"root_ref"`
	RefPrefix string `json:"ref_prefix"`
	BaseOID   string `json:"base_oid"`
}

type ExchangeContext struct {
	Heads map[string]string `json:"heads,omitempty"`
	Tags  map[string]string `json:"tags,omitempty"`
}

type Return struct {
	Protocol    string      `json:"protocol"`
	ExchangeID  string      `json:"exchange_id"`
	CreatedAt   time.Time   `json:"created_at"`
	ToolVersion string      `json:"tool_version"`
	BundleFile  string      `json:"bundle_file"`
	Task        ReturnTask  `json:"task"`
	Updates     []RefUpdate `json:"updates"`
}

type ReturnTask struct {
	RootRef string `json:"root_ref"`
	BaseOID string `json:"base_oid"`
}

type RefUpdate struct {
	Ref    string `json:"ref"`
	OldOID string `json:"old_oid"`
	NewOID string `json:"new_oid"`
}
