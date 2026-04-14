package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	ghyllcontext "github.com/witlox/ghyll/context"
)

// REPL runs the interactive read-eval-print loop.
func REPL(sess *Session, input io.Reader) {
	scanner := bufio.NewScanner(input)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\nshutting down...")
		// Create final checkpoint on graceful shutdown
		if sess.ctxManager.Turn() > 0 {
			_ = sess.createCheckpoint(ghyllcontext.CheckpointRequest{
				SessionID:   sess.sessionID,
				Turn:        sess.ctxManager.Turn(),
				ActiveModel: sess.activeModel,
				Summary:     "session ended (signal)",
				Messages:    sess.ctxManager.Messages(),
				Reason:      "shutdown",
			})
		}
		os.Exit(0)
	}()

	for {
		fmt.Print(sess.Prompt())

		if !scanner.Scan() {
			break // EOF
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Handle commands
		switch {
		case line == "/exit" || line == "/quit":
			// Final checkpoint on clean exit
			if sess.ctxManager.Turn() > 0 {
				_ = sess.createCheckpoint(ghyllcontext.CheckpointRequest{
					SessionID:   sess.sessionID,
					Turn:        sess.ctxManager.Turn(),
					ActiveModel: sess.activeModel,
					Summary:     "session ended",
					Messages:    sess.ctxManager.Messages(),
					Reason:      "shutdown",
				})
			}
			fmt.Println("goodbye")
			return
		case line == "/deep":
			if sess.modelLocked {
				sess.output("ℹ /deep ignored, model locked via --model flag")
			} else {
				sess.deepOverride = true
				sess.output("switched to deep tier")
			}
			continue
		case line == "/plan":
			if sess.planMode {
				sess.output("plan mode already active")
			} else {
				sess.planMode = true
				sess.output("plan mode activated")
			}
			continue
		case line == "/fast":
			if sess.modelLocked {
				sess.output("ℹ /fast ignored, model locked via --model flag")
			} else {
				sess.deepOverride = false
				sess.planMode = false
				sess.output("auto-routing restored, plan mode off")
			}
			continue
		case line == "/status":
			fmt.Printf("model: %s (locked: %v, deep: %v, plan: %v)\n",
				sess.activeModel, sess.modelLocked, sess.deepOverride, sess.planMode)
			fmt.Printf("turn: %d, tool_depth: %d\n",
				sess.ctxManager.Turn(), sess.toolDepth)
			continue
		case strings.HasPrefix(line, "/"):
			// Check workflow commands (invariant 49: inject as user message)
			cmdName := strings.TrimPrefix(line, "/")
			if sess.wf != nil {
				if content, ok := sess.wf.Commands[cmdName]; ok {
					// Inject command content as user input and process as a turn
					_, err := sess.Turn(content)
					if err != nil {
						sess.renderer.RenderError(err.Error())
					}
					continue
				}
			}
			sess.output(fmt.Sprintf("unknown command: %s", line))
			continue
		}

		// Execute turn — response is already streamed to terminal via renderer
		_, err := sess.Turn(line)
		if err != nil {
			sess.renderer.RenderError(err.Error())
			continue
		}
	}
}
