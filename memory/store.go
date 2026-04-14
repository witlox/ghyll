package memory

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	_ "modernc.org/sqlite"
)

var (
	ErrHashMismatch     = errors.New("memory: recomputed hash does not match")
	ErrSignatureInvalid = errors.New("memory: ed25519 signature verification failed")
	ErrChainBroken      = errors.New("memory: parent hash does not match previous checkpoint")
	ErrUnknownKey       = errors.New("memory: no public key found for device")
	ErrStoreReadOnly    = errors.New("memory: checkpoint store is append-only")
	ErrEmbedderUnavail  = errors.New("memory: embedding model not available")
)

// SearchResult is a checkpoint with similarity score.
type SearchResult struct {
	Checkpoint Checkpoint
	Similarity float64
}

// Store is the sqlite-backed checkpoint store.
// Invariant 3: append-only — no UPDATE or DELETE.
type Store struct {
	db *sql.DB
}

// OpenStore opens or creates the sqlite checkpoint store.
func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("memory: open database: %w", err)
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS checkpoints (
			hash       TEXT PRIMARY KEY,
			parent     TEXT NOT NULL,
			device     TEXT NOT NULL,
			author     TEXT NOT NULL,
			ts         INTEGER NOT NULL,
			repo       TEXT NOT NULL DEFAULT '',
			branch     TEXT NOT NULL DEFAULT '',
			session    TEXT NOT NULL,
			turn       INTEGER NOT NULL,
			model      TEXT NOT NULL,
			summary    TEXT NOT NULL,
			embedding  BLOB NOT NULL DEFAULT x'',
			files      TEXT NOT NULL DEFAULT '[]',
			tools      TEXT NOT NULL DEFAULT '[]',
			injections TEXT,
			sig        TEXT NOT NULL,
			verified   INTEGER DEFAULT 1,
			imported   INTEGER DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_checkpoints_session ON checkpoints(session);
		CREATE INDEX IF NOT EXISTS idx_checkpoints_device ON checkpoints(device);
		CREATE INDEX IF NOT EXISTS idx_checkpoints_repo ON checkpoints(repo);
	`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("memory: create schema: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// Append adds a checkpoint to the store. Idempotent (invariant 14).
func (s *Store) Append(cp *Checkpoint) error {
	files := cp.FilesTouched
	if files == nil {
		files = []string{}
	}
	tools := cp.ToolsUsed
	if tools == nil {
		tools = []string{}
	}
	filesJSON, _ := json.Marshal(files)
	toolsJSON, _ := json.Marshal(tools)
	embBytes := embedToBytes(cp.Embedding)
	if embBytes == nil {
		embBytes = []byte{}
	}

	var injectionsJSON *string
	if len(cp.InjectionSig) > 0 {
		b, _ := json.Marshal(cp.InjectionSig)
		s := string(b)
		injectionsJSON = &s
	}

	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO checkpoints
			(hash, parent, device, author, ts, repo, branch, session, turn, model,
			 summary, embedding, files, tools, injections, sig)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cp.Hash, cp.ParentHash, cp.DeviceID, cp.AuthorID, cp.Timestamp,
		cp.RepoRemote, cp.Branch, cp.SessionID, cp.Turn, cp.ActiveModel,
		cp.Summary, embBytes, string(filesJSON), string(toolsJSON),
		injectionsJSON, cp.Signature,
	)
	if err != nil {
		return fmt.Errorf("memory: append checkpoint: %w", err)
	}
	return nil
}

// GetByHash retrieves a single checkpoint by its hash.
func (s *Store) GetByHash(hash string) (*Checkpoint, error) {
	row := s.db.QueryRow(`SELECT hash, parent, device, author, ts, repo, branch,
		session, turn, model, summary, embedding, files, tools, injections, sig
		FROM checkpoints WHERE hash = ?`, hash)
	return scanCheckpoint(row)
}

// ListBySession returns all checkpoints for a session, ordered by turn.
func (s *Store) ListBySession(sessionID string) ([]Checkpoint, error) {
	rows, err := s.db.Query(`SELECT hash, parent, device, author, ts, repo, branch,
		session, turn, model, summary, embedding, files, tools, injections, sig
		FROM checkpoints WHERE session = ? ORDER BY turn`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("memory: list by session: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []Checkpoint
	for rows.Next() {
		cp, err := scanCheckpointRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *cp)
	}
	return result, rows.Err()
}

// ListAll returns all checkpoints ordered by timestamp.
func (s *Store) ListAll() ([]Checkpoint, error) {
	rows, err := s.db.Query(`SELECT hash, parent, device, author, ts, repo, branch,
		session, turn, model, summary, embedding, files, tools, injections, sig
		FROM checkpoints ORDER BY ts`)
	if err != nil {
		return nil, fmt.Errorf("memory: list all: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []Checkpoint
	for rows.Next() {
		cp, err := scanCheckpointRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *cp)
	}
	return result, rows.Err()
}

// LatestBySession returns the most recent checkpoint for a session.
func (s *Store) LatestBySession(sessionID string) (*Checkpoint, error) {
	row := s.db.QueryRow(`SELECT hash, parent, device, author, ts, repo, branch,
		session, turn, model, summary, embedding, files, tools, injections, sig
		FROM checkpoints WHERE session = ? ORDER BY turn DESC LIMIT 1`, sessionID)
	return scanCheckpoint(row)
}

// LatestByRepo returns the most recent checkpoint for a repo, by timestamp.
// Used for session resume (invariant 42).
func (s *Store) LatestByRepo(repoRemote string) (*Checkpoint, error) {
	row := s.db.QueryRow(`SELECT hash, parent, device, author, ts, repo, branch,
		session, turn, model, summary, embedding, files, tools, injections, sig
		FROM checkpoints WHERE repo = ? ORDER BY ts DESC LIMIT 1`, repoRemote)
	return scanCheckpoint(row)
}

// SearchByEmbedding finds the top-k most similar checkpoints via brute-force cosine similarity.
func (s *Store) SearchByEmbedding(query []float32, repoHash string, topK int) ([]SearchResult, error) {
	rows, err := s.db.Query(`SELECT hash, parent, device, author, ts, repo, branch,
		session, turn, model, summary, embedding, files, tools, injections, sig
		FROM checkpoints WHERE repo = ?`, repoHash)
	if err != nil {
		return nil, fmt.Errorf("memory: search: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []SearchResult
	for rows.Next() {
		cp, err := scanCheckpointRow(rows)
		if err != nil {
			return nil, err
		}
		sim := cosineSimilarity(query, cp.Embedding)
		results = append(results, SearchResult{Checkpoint: *cp, Similarity: sim})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Sort by similarity descending, take top-k
	sortBySimDesc(results)
	if len(results) > topK {
		results = results[:topK]
	}
	return results, nil
}

func scanCheckpoint(row *sql.Row) (*Checkpoint, error) {
	var cp Checkpoint
	var embBytes []byte
	var filesJSON, toolsJSON string
	var injectionsJSON *string

	if err := row.Scan(&cp.Hash, &cp.ParentHash, &cp.DeviceID, &cp.AuthorID,
		&cp.Timestamp, &cp.RepoRemote, &cp.Branch, &cp.SessionID, &cp.Turn,
		&cp.ActiveModel, &cp.Summary, &embBytes, &filesJSON, &toolsJSON,
		&injectionsJSON, &cp.Signature); err != nil {
		return nil, fmt.Errorf("memory: scan checkpoint: %w", err)
	}

	cp.Embedding = bytesToEmbed(embBytes)
	_ = json.Unmarshal([]byte(filesJSON), &cp.FilesTouched)
	_ = json.Unmarshal([]byte(toolsJSON), &cp.ToolsUsed)
	if injectionsJSON != nil {
		_ = json.Unmarshal([]byte(*injectionsJSON), &cp.InjectionSig)
	}
	return &cp, nil
}

func scanCheckpointRow(rows *sql.Rows) (*Checkpoint, error) {
	var cp Checkpoint
	var embBytes []byte
	var filesJSON, toolsJSON string
	var injectionsJSON *string

	if err := rows.Scan(&cp.Hash, &cp.ParentHash, &cp.DeviceID, &cp.AuthorID,
		&cp.Timestamp, &cp.RepoRemote, &cp.Branch, &cp.SessionID, &cp.Turn,
		&cp.ActiveModel, &cp.Summary, &embBytes, &filesJSON, &toolsJSON,
		&injectionsJSON, &cp.Signature); err != nil {
		return nil, fmt.Errorf("memory: scan checkpoint: %w", err)
	}

	cp.Embedding = bytesToEmbed(embBytes)
	_ = json.Unmarshal([]byte(filesJSON), &cp.FilesTouched)
	_ = json.Unmarshal([]byte(toolsJSON), &cp.ToolsUsed)
	if injectionsJSON != nil {
		_ = json.Unmarshal([]byte(*injectionsJSON), &cp.InjectionSig)
	}
	return &cp, nil
}

func embedToBytes(emb []float32) []byte {
	if len(emb) == 0 {
		return nil
	}
	buf := make([]byte, len(emb)*4)
	for i, v := range emb {
		bits := math.Float32bits(v)
		buf[i*4] = byte(bits)
		buf[i*4+1] = byte(bits >> 8)
		buf[i*4+2] = byte(bits >> 16)
		buf[i*4+3] = byte(bits >> 24)
	}
	return buf
}

func bytesToEmbed(b []byte) []float32 {
	if len(b) == 0 {
		return nil
	}
	emb := make([]float32, len(b)/4)
	for i := range emb {
		bits := uint32(b[i*4]) | uint32(b[i*4+1])<<8 | uint32(b[i*4+2])<<16 | uint32(b[i*4+3])<<24
		emb[i] = math.Float32frombits(bits)
	}
	return emb
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func sortBySimDesc(results []SearchResult) {
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].Similarity > results[j-1].Similarity; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
}
