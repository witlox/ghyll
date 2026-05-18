package dialect

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/witlox/ghyll/types"
)

var ErrParseToolCall = errors.New("dialect: failed to parse tool call from response")

// parseOpenAIToolCalls parses the standard OpenAI tool_calls format.
// Shared between dialects that use the same format via SGLang.
// Validation-pass-8 D15: wrap underlying error via %w so operators
// debugging a misbehaving quantized backend can see the actual
// JSON failure.
func parseOpenAIToolCalls(raw json.RawMessage) ([]types.ToolCall, error) {
	var calls []types.ToolCall
	if err := json.Unmarshal(raw, &calls); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrParseToolCall, err)
	}
	return calls, nil
}
