# Design Conversation — Ghyll

Sanitized analyst discussion. Identifying information removed.

## Origin

The project originated from an infrastructure sizing exercise: how many developers could be served by open-weight models (GLM-5, MiniMax M2.5, Kimi K2) running on a Cray EX supercomputer with GH200 Grace Hopper nodes?

## Infrastructure Context

- Cray EX topology: 4 GH200 modules per node, 2 nodes per blade, 7 blades per chassis, 8 chassis per rack
- GH200 specs: 96 GB HBM3 + 480 GB LPDDR5X per module, Slingshot 200 interconnect (1 NIC per module), NVLink backplane within each node
- GLM-5 FP8 (~372 GB) fits on 1 blade (2 nodes, 8 GH200, 768 GB HBM3)
- MiniMax M2.5 (~457 GB BF16, much less quantized) fits on 1 node (4 GH200, 384 GB HBM3)
- Kimi K2 Thinking INT4 (~594 GB) fits on 1 blade

## Model Selection

Comparison against Claude Opus 4.6 for coding assistant use:

- GLM-5: 77.8% SWE-bench Verified (vs Opus 4.6 at 80.8%), 200K context, 744B/40B active MoE
- MiniMax M2.5: 80.2% SWE-bench Verified, 1M context, 230B/10B active MoE — closest to Opus quality
- Kimi K2: 65.8-71.3% SWE-bench, 256K context, 1T/32B active MoE — strong agentic capabilities

Key finding: MiniMax M2.5 at 80.2% SWE-bench is within 0.6 points of Opus 4.6, at 1/20th the cost, and fits in a single compute node.

## Two-Tier Design

Rather than running one model for everything, use two tiers:
- Fast tier (M2.5): routine coding — autocomplete, tests, straightforward debugging
- Deep tier (GLM-5): complex reasoning, architecture, multi-file refactoring

Routing based on context depth and task complexity, not an external classifier.

## Capacity Estimates

For ~1,000 developers with coding assistant usage patterns:
- Half a chassis (~4 blades + nodes) is sufficient
- Cost: $250K-$500K CapEx vs ~$100-150K/month for cloud API
- Break-even: 2-4 months

## Why Build a New Tool

Existing tools (Claude Code, OpenCode, Gemini CLI) are designed for cloud API backends with broad model support. The abstraction tax of "supports everything" degrades performance for specific models. Key requirements:

1. Hyper-optimized prompts and tool-calling for 2-3 specific models
2. No wrapper layers — direct OS integration for tools
3. Always-yolo (SRT handles sandboxing)
4. Model-aware compaction and context management
5. Drift-aware memory with vector embeddings

## Memory Architecture

Evolved through several iterations:

1. Started as simple sqlite checkpoints
2. Added Merkle DAG hash chain for tamper evidence
3. Added ed25519 signatures for attribution
4. Chose git orphan branch as sync transport (no vault service needed)
5. Added drift detection via cosine similarity of embeddings
6. Added backfill from team memory when drift exceeds threshold

Key insight: git is already a content-addressable Merkle DAG. Using an orphan branch in the project's own repo provides authenticated, encrypted, conflict-free, offline-first sync with zero additional infrastructure.

## Why Not Existing Tools

- **Claude Code / OpenClaude**: Tightly coupled to Anthropic's API format. Multi-provider support bolted on. 160K+ lines of TypeScript with Node.js runtime dependency.
- **OpenCode**: Multi-provider but "quite a big mess." Tries to support everything, optimizes nothing.
- **Gemini CLI**: Cleaner architecture but Gemini-first. Would need substantial rework for open models.
- **None of them**: store memory in git, do drift detection, provide tamper-evident team knowledge, or optimize per-model.

## Technology Decisions

- Go over TypeScript (no runtime dependency) and Rust (ecosystem complexity)
- ONNX Runtime for embeddings (lazy download, ~60MB model)
- Concrete dialect modules over provider abstraction (no interfaces)
- Checkpoint-based handoff for model switching (lossy but token-efficient)
- SRT (Anthropic's Sandbox Runtime) for sandboxing — Seatbelt on macOS, bubblewrap on Linux

## Name

Ghyll: a narrow mountain ravine. Focused, channeled.
