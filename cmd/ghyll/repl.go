package main

import (
	"bufio"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	ghyllcontext "github.com/witlox/ghyll/context"
	"github.com/witlox/ghyll/ui"
)

// REPL runs the interactive read-eval-print loop.
func REPL(sess *Session, input io.Reader) {
	scanner := bufio.NewScanner(input)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		ui.Info("\nshutting down...")
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
		ui.Print(sess.Prompt())

		if !scanner.Scan() {
			break // EOF
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Built-in slash commands. /quit is a REPL-only alias for /exit.
		if line == "/quit" {
			line = "/exit"
		}
		if res := sess.DispatchSlashCommand(line); res.Handled {
			if res.ExitRequested {
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
				ui.Info("goodbye")
				return
			}
			if res.Output != "" {
				sess.output(res.Output)
			}
			continue
		}

		// Non-built-in slash commands (workflow-defined).
		if strings.HasPrefix(line, "/") {
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
			sess.output("unknown command: " + line)
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
