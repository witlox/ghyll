package main

import "fmt"

// defaultMaxToolResultBytes is the per-tool-result byte cap when
// [tools].max_result_bytes is unset (0). 8 KiB is roughly
// "useful but not bloated": enough for a small file dump, a
// dozen-line bash output, or a few hundred lines of grep — but
// hard-stops the 50 KB `find /` outputs and 100 KB read_file of
// generated artifacts that previously rode straight into context.
const defaultMaxToolResultBytes = 8 * 1024

// capToolResult truncates a tool result to maxBytes (or
// defaultMaxToolResultBytes when maxBytes is 0), appending a
// machine-and-human-readable marker so:
//   - the operator inspecting .ghyll/ghyll.log knows truncation
//     happened and can raise the cap;
//   - the model SEES the marker and knows the truncated portion
//     exists, rather than treating the partial output as the
//     complete file/listing/etc.
//
// maxBytes == -1 disables truncation entirely (operator opt-out).
// Negative-not-minus-one values are treated as the default to be
// safe — operators reaching for a custom value typed -1 by
// intent, not -50.
func capToolResult(s string, maxBytes int) string {
	if maxBytes == -1 {
		return s
	}
	if maxBytes <= 0 {
		maxBytes = defaultMaxToolResultBytes
	}
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + fmt.Sprintf(
		"\n\n[ghyll: truncated tool result — kept %d of %d bytes; raise [tools].max_result_bytes (or set -1) to see more]",
		maxBytes, len(s),
	)
}
