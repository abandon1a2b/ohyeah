package collector

type UploadBatch struct {
	ProtocolVersion int        `json:"protocolVersion"`
	SourceID        string     `json:"sourceId"`
	BatchID         string     `json:"batchId"`
	ExpectedVersion int64      `json:"expectedVersion"`
	ScopeRevision   string     `json:"scopeRevision"`
	Envelopes       []Envelope `json:"envelopes"`
}

type UploadReceipt struct {
	SourceID     string `json:"sourceId"`
	BatchID      string `json:"batchId"`
	Version      int64  `json:"version"`
	RunID        int64  `json:"runId"`
	Changed      int    `json:"changed"`
	Deleted      int    `json:"deleted"`
	Replayed     bool   `json:"replayed"`
	IndexPending bool   `json:"indexPending"`
}
