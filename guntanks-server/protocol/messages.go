package protocol

type Message struct {
	Type      string         `json:"type"`
	RequestID string         `json:"request_id,omitempty"`
	BattleID  string         `json:"battle_id,omitempty"`
	Revision  uint64         `json:"revision,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
}
type Event struct {
	Type     string `json:"type"`
	BattleID string `json:"battle_id,omitempty"`
	Revision uint64 `json:"revision,omitempty"`
	EventSeq uint64 `json:"event_seq,omitempty"`
	Payload  any    `json:"payload,omitempty"`
}
type Error struct {
	Code, Message, RequestID string
	Retryable                bool
}
