package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/types"
)

// memorySearchTool is the in-session implementation of the
// `memory_search` tool. The model calls it to recall summaries of
// prior sessions stored in ~/.ghyll/memory.db — decisions made, bugs
// fixed, ongoing work, etc. This is the operator-visible memory the
// embedder-driven backfill DOESN'T expose when the embedder is
// unavailable (CGO-less release binaries on hosts without ONNX
// Runtime fall into this case).
//
// Two search modes share the same backend the CLI walks (the same
// helpers as cmdMemorySearch):
//
//   - Hash-prefix: single hex-only token of >=6 chars matches by
//     HasPrefix against cp.Hash. Model can paste a hash it saw in a
//     prior tool call to inspect that specific checkpoint.
//   - Text-search: tokens are matched against the summary; at least
//     half the query terms must appear. Mirrors the CLI's `memory
//     search` behavior so the model gets the same recall.
//
// limit caps the result count. Default 5 keeps the tool result
// payload bounded so a wide query doesn't blow the model's per-call
// context budget. Clamped to [1, 20] — any larger and the result is
// likely noise the model can't reason about.
func (s *Session) memorySearchTool(query string, limit int) types.ToolResult {
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return types.ToolResult{Error: "memory_search requires a non-empty query"}
	}

	checkpoints, err := s.store.ListAll()
	if err != nil {
		return types.ToolResult{Error: fmt.Sprintf("memory store read failed: %v", err)}
	}

	queryLower := strings.ToLower(query)
	queryTerms := strings.Fields(queryLower)

	var matches []memory.Checkpoint
	if len(queryTerms) == 1 && len(queryTerms[0]) >= 6 && isHexPrefix(queryTerms[0]) {
		// Hash-prefix path.
		for _, cp := range checkpoints {
			if strings.HasPrefix(strings.ToLower(cp.Hash), queryTerms[0]) {
				matches = append(matches, cp)
				if len(matches) >= limit {
					break
				}
			}
		}
	} else {
		// Text-search path. Reuse the half-overlap rule from
		// cmdMemorySearch so model + operator see identical recall.
		for _, cp := range checkpoints {
			summaryLower := strings.ToLower(cp.Summary)
			matched := 0
			for _, term := range queryTerms {
				if strings.Contains(summaryLower, term) {
					matched++
				}
			}
			if matched > 0 && matched >= len(queryTerms)/2 {
				matches = append(matches, cp)
				if len(matches) >= limit {
					break
				}
			}
		}
	}

	if len(matches) == 0 {
		return types.ToolResult{Output: "no matching checkpoints"}
	}

	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "Found %d checkpoint(s) for %q:\n\n", len(matches), query)
	for _, cp := range matches {
		ts := time.Unix(0, cp.Timestamp)
		if cp.Timestamp < 1e12 {
			ts = time.Unix(cp.Timestamp, 0)
		}
		_, _ = fmt.Fprintf(&sb, "%s  %s  @%s  turn %d  [%s]\n  %s\n\n",
			cp.Hash[:12],
			ts.Format("2006-01-02 15:04"),
			cp.AuthorID,
			cp.Turn,
			cp.ActiveModel,
			cp.Summary,
		)
	}
	return types.ToolResult{Output: sb.String()}
}
