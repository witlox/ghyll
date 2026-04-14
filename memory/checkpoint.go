package memory

// Checkpoint is the immutable unit of session memory.
// Invariant 3: never modified after creation.
// Invariant 4: Hash = hex(sha256(canonical JSON of all fields except Hash and Signature)).
type Checkpoint struct {
	Version      int        `json:"v"`
	Hash         string     `json:"hash"`
	ParentHash   string     `json:"parent"`
	DeviceID     string     `json:"device"`
	AuthorID     string     `json:"author"`
	Timestamp    int64      `json:"ts"`
	RepoRemote   string     `json:"repo"`
	Branch       string     `json:"branch"`
	SessionID    string     `json:"session"`
	Turn         int        `json:"turn"`
	ActiveModel  string     `json:"model"`
	PlanMode     bool       `json:"plan_mode,omitempty"`    // v2: plan mode state for handoff
	ResumedFrom  *ResumeRef `json:"resumed_from,omitempty"` // v2: predecessor session link
	Summary      string     `json:"summary"`
	Embedding    []float32  `json:"emb"`
	FilesTouched []string   `json:"files"`
	ToolsUsed    []string   `json:"tools"`
	InjectionSig []string   `json:"injections,omitempty"`
	Signature    string     `json:"sig"`
}

// ResumeRef links a resumed session to its predecessor.
type ResumeRef struct {
	SessionID      string `json:"session"`
	CheckpointHash string `json:"hash"`
}
