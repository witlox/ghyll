# Injection Detection

ghyll scans conversation turns for prompt injection patterns at checkpoint creation time. This is **detection only** --- SRT handles enforcement at the OS level.

## What's Detected

| Pattern | Examples |
|---------|----------|
| `instruction_override` | "ignore previous instructions", "you are now", "act as if" |
| `sensitive_path` | `~/.ssh/`, `/etc/shadow`, `.env`, `id_rsa`, `private_key` |
| `base64_payload` | Long base64-encoded strings that decode to valid UTF-8 |
| `system_prompt_modify` | "modify your system prompt", "rewrite your prompt" |

## Scan Scope

Only **user** and **tool** messages are scanned. Assistant (model) responses are not scanned --- if the model talks about injection patterns, it's not a false positive concern.

## What Happens

When injection signals are detected:

1. The signal is recorded in the checkpoint's `injections` field
2. A warning is displayed: `checkpoint 3: injection signal in turn 7`
3. The checkpoint is still created (detection, not prevention)
4. SRT blocks any actual dangerous operations at the OS level

## Why Detection Only

ghyll executes tools directly (always-yolo). Blocking at the ghyll level would create a false sense of security and could be bypassed. SRT provides OS-level sandboxing (Seatbelt on macOS, bubblewrap on Linux) that cannot be circumvented from user space, regardless of what ghyll executes.
